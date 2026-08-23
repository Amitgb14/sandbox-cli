"""Three agents that hand work to each other, and a gate that decides.

The Python twin of the TypeScript client's `travel-planner.ts`. Async, because
the fan-out is the point: two specialists research in parallel and a coordinator
combines what they produced.

Each agent works in its own branch's worktree, which is the isolation unit — one
tree, one container, one agent. So the coordinator **cannot see** what the
specialists wrote, and the artifacts cross deliberately, through this process.
Telling it to "assume the files are there" is the natural thing to write and it
fails silently: the agent invents plausible inputs and the report reads exactly
like one built from the real thing.

    python3 examples/travel_planner.py
"""

from __future__ import annotations

import asyncio
import base64
import sys
from dataclasses import dataclass

from sandbox_cli import Outcome, RunCancelled
from sandbox_cli.aio import AsyncStudio, AsyncWorkspace

AGENT = "claude"
FALLBACK = ["codex"]

TRIP = dict(origin="SFO", destination="NRT", depart="2026-10-15",
            ret="2026-10-22", adults=2, budget_usd=2500)

BRIEF = f"""# Trip Brief
- Origin: {TRIP['origin']}
- Destination: {TRIP['destination']}
- Depart: {TRIP['depart']}
- Return: {TRIP['ret']}
- Travelers: {TRIP['adults']} adults
- Rough total budget: ${TRIP['budget_usd']}
"""


@dataclass
class Finding:
    """What the gate needs to know about one specialist, and nothing else."""

    branch: str
    agent: str
    artifact: str
    produced: bool
    note: str


async def put(ws: AsyncWorkspace, path: str, content: str) -> None:
    """Write a file into a workspace from here.

    Base64, not a heredoc. An artifact written by an agent is attacker-controlled
    as far as this script is concerned, and a heredoc built by interpolation is
    one `EOF` line away from being the next command — in a container, as root's
    entrypoint would see it. Base64 has no shell metacharacters, so nothing in
    the content can change what runs.
    """
    b64 = base64.b64encode(content.encode()).decode()
    r = await ws.run(["sh", "-c", f"printf %s '{b64}' | base64 -d > {path}"])
    if r.exit_code != 0:
        raise RuntimeError(f"writing {path}: {r.stderr.strip()}")


async def get(ws: AsyncWorkspace, path: str) -> str:
    """Read a file back out. Empty when it is not there.

    Base64 on the way back too, and not for symmetry: `stdout` is the run's log
    *lines* joined, so a file's trailing newline cannot survive `cat` — measured,
    64 bytes back for 65 written. Fine for reading output, wrong for moving a
    file, and the missing byte is one no "did it work" check would notice.
    """
    r = await ws.run(["sh", "-c", f"base64 < {path} 2>/dev/null | tr -d '\\n' || true"])
    raw = r.stdout.strip()
    return base64.b64decode(raw).decode() if raw else ""


async def specialist(repo, branch: str, artifact: str, prompt: str) -> tuple[AsyncWorkspace, Finding]:
    ws = await repo.workspace(branch)
    # A finished run keeps its branch's container name until something reaps it,
    # so without this the second run of this script is refused.
    await ws.clear_finished()
    await put(ws, "trip-brief.md", BRIEF)

    try:
        out: Outcome = await ws.agent(AGENT, prompt, fallback=FALLBACK, timeout=12 * 60)
    except RunCancelled as cancelled:
        # The wait was cancelled and the SDK stopped the run before raising. The
        # container existed, so silence here would leave an agent working with
        # nobody reading the result.
        return ws, Finding(branch, AGENT, artifact, False, f"cancelled ({cancelled.run['id'][:12]})")

    if out.stopped:
        # Not a verdict. A container somebody interrupted has no opinion about the
        # work, and reporting it as failure is how a deadline becomes a bug report.
        return ws, Finding(branch, out.agent or AGENT, artifact, False, "outlived its deadline")
    if out.exit_code != 0:
        last = out.stderr.strip().splitlines()
        return ws, Finding(branch, out.agent or AGENT, artifact, False,
                           last[-1] if last else f"exit {out.exit_code}")

    # Asked of the filesystem rather than of the agent: an agent that reports
    # success having written nothing is the commonest failure this gate exists to
    # catch, and the one it cannot be told about.
    produced = (await get(ws, artifact)).strip() != ""
    return ws, Finding(branch, out.agent or AGENT, artifact, produced,
                       "ok" if produced else f"finished without writing {artifact}")


FLIGHTS = f"""You are the flight search specialist. Read trip-brief.md.

Research realistic options from {TRIP['origin']} to {TRIP['destination']} for those dates,
preferring nonstop or one stop. Write flights.json:

{{"options": [{{"id": "F1", "airline": "...", "price_usd": 850, "stops": 0}}], "recommended": "F1"}}

Write the file and stop."""

HOTELS = """You are the hotel search specialist. Read trip-brief.md.

Find three or four mid-range hotels in Tokyo near transit, $120-250 a night.
Write hotels.json:

{"options": [{"id": "H1", "name": "...", "price_per_night_usd": 180}], "recommended": "H1"}

Write the file and stop."""


async def main() -> int:
    studio = await AsyncStudio.connect()
    # A lookup, not a registration: `add_project` would permanently add this
    # directory to the daemon's registry as a side effect of running an example,
    # and against a remote daemon it would post a local path that daemon cannot
    # resolve.
    repo = await studio.project()          # the repository this script is standing in

    # In parallel, because the isolation unit is the branch: two agents in one
    # tree would be a data race with a filesystem in the middle; two agents in
    # two trees are simply two runs. return_exceptions rather than bare gather —
    # one specialist failing is not a reason to lose the other's work, and the
    # coordinator is told what is missing instead.
    outcomes = await asyncio.gather(
        specialist(repo, "agent-flights", "flights.json", FLIGHTS),
        specialist(repo, "agent-hotels", "hotels.json", HOTELS),
        return_exceptions=True,
    )

    sources: list[tuple[AsyncWorkspace, Finding]] = []
    findings: list[Finding] = []
    for branch, artifact, result in (("agent-flights", "flights.json", outcomes[0]),
                                     ("agent-hotels", "hotels.json", outcomes[1])):
        if isinstance(result, BaseException):
            findings.append(Finding(branch, "?", artifact, False, repr(result)))
            continue
        sources.append(result)
        findings.append(result[1])

    for f in findings:
        print(f"{'OK  ' if f.produced else 'SKIP'} {f.branch:<15} {f.agent:<7} {f.note}")

    # The handover: read each artifact out of the tree that produced it and write
    # it into the coordinator's. This is the step that makes this a workflow
    # rather than three agents guessing in parallel.
    coord = await repo.workspace("agent-coordinator")
    await coord.clear_finished()
    await put(coord, "trip-brief.md", BRIEF)

    handed: list[str] = []
    for ws, f in sources:
        if not f.produced:
            continue
        await put(coord, f.artifact, await get(ws, f.artifact))
        handed.append(f.artifact)
    print(f"handed over: {', '.join(handed) or 'nothing'}")

    task = ("Pick the best flight and hotel combination that stays near the budget."
            if len(handed) == 2 else
            "Some specialist output is missing. Say so explicitly in the itinerary and work "
            "with what is here — do not invent the missing file's contents.")

    final = await coord.agent(
        AGENT,
        f"You are the travel coordinator. You have trip-brief.md"
        f"{' and ' + ' and '.join(handed) if handed else ''}.\n\n{task}\n\n"
        "Write itinerary.md for a human, and recommendation.json with the final choice and "
        "estimated total. Any booking step is SIMULATED — do not attempt a real payment.",
        fallback=FALLBACK, timeout=10 * 60,
    )

    print(f"\ncoordinator exited {final.exit_code}")
    print((await get(coord, "itinerary.md"))[:2000])

    # The decision, and the reason it asks two questions: the coordinator's exit
    # code says whether it finished, and the file says whether it decided
    # anything. Either alone has been enough to wave through an empty report.
    decided = (await get(coord, "recommendation.json")).strip() != ""
    if final.exit_code == 0 and decided:
        return 0
    if not decided:
        print("no recommendation was produced", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(asyncio.run(main()))
