import { Studio, WaitError, type Outcome, type Workspace } from "@sandbox-cli/sdk";

/**
 * Three agents that hand work to each other.
 *
 * Two specialists research in parallel — one flights, one hotels — and a
 * coordinator combines what they produced. It is the shape most "multi-agent"
 * workflows actually want, and the interesting part is not the fan-out but what
 * happens between the two halves.
 *
 * Each agent works in its own branch's worktree, because that is the isolation
 * unit: one tree, one container, one agent. Which means the coordinator **cannot
 * see** what the specialists wrote — different trees, and this SDK has no file
 * API to reach into one. So the artifacts cross deliberately, through the host,
 * by reading stdout from one workspace and writing it into another. Two `run`
 * calls and no new machinery.
 *
 * The alternative — telling the coordinator to "assume the files exist" — is
 * worth naming because it is the natural thing to write and it fails silently:
 * the agent invents plausible inputs and the report reads exactly like one built
 * from the real ones.
 */

const TRIP = {
  origin: "SFO",
  destination: "NRT", // Tokyo
  depart: "2026-10-15",
  return: "2026-10-22",
  adults: 2,
  budgetUsd: 2500,
};

const BRIEF = `# Trip Brief
- Origin: ${TRIP.origin}
- Destination: ${TRIP.destination}
- Depart: ${TRIP.depart}
- Return: ${TRIP.return}
- Travelers: ${TRIP.adults} adults
- Rough total budget: $${TRIP.budgetUsd}
- Preferences: nonstop or one stop, mid-range hotels near transit, flexible on airline
`;

const studio = await Studio.connect();
const repo = await studio.addProject(); // the repository this script is in
const AGENT = "claude";

/** Write a file into a workspace from the host.
 *
 * Base64 rather than a heredoc, and that is the one detail in this file worth
 * copying. An artifact written by an agent is attacker-controlled as far as this
 * script is concerned, and a heredoc built by string interpolation is one `EOF`
 * line away from being the next command — in a container, as root's entrypoint
 * would see it. Base64 has no shell metacharacters, so nothing in the content
 * can change what runs. (It travels in the argv, so this is for artifacts rather
 * than for large files.) */
async function put(ws: Workspace, path: string, content: string): Promise<void> {
  const b64 = Buffer.from(content, "utf8").toString("base64");
  const res = await ws.run(["sh", "-c", `printf %s '${b64}' | base64 -d > ${path}`]);
  if (res.exitCode !== 0) throw new Error(`writing ${path}: ${res.stderr.trim()}`);
}

/** Read a file out of a workspace. Empty when it is not there — an agent that
 *  did not produce its artifact is a fact the coordinator should be told.
 *
 * Base64 on the way back too, and not for symmetry: `Outcome.stdout` is the
 * run's log *lines* joined with newlines, so a file's trailing newline cannot
 * survive `cat` — measured, 64 bytes back for 65 written. Fine for reading
 * output, wrong for moving a file, and the difference is one byte that no test
 * of "did it work" would notice. */
async function get(ws: Workspace, path: string): Promise<string> {
  const res = await ws.run(["sh", "-c", `base64 < ${path} 2>/dev/null | tr -d '\\n' || true`]);
  return Buffer.from(res.stdout.trim(), "base64").toString("utf8");
}

/** One specialist: its own branch, its own container, its own conversation. */
async function specialist(branch: string, prompt: string): Promise<{ ws: Workspace; out: Outcome }> {
  const ws = await repo.workspace(branch);
  // A finished run keeps its branch's container name until something reaps it,
  // so without this the second run of this script is refused with a 409.
  await ws.clearFinished();
  await put(ws, "trip-brief.md", BRIEF);
  try {
    const out = await ws.agent(AGENT, prompt, {
      timeoutMs: 12 * 60_000,
      fallback: ["codex"],
      // Egress is whatever the daemon's posture allows — under the default
      // allowlist these agents work from what the model already knows rather
      // than from a live API. `allow: ["api.example.com"]` widens it for one
      // run, and can only ever add to the daemon's list, never loosen it.
    });
    return { ws, out };
  } catch (err) {
    // The launch succeeded and the wait did not, so a container is still out
    // there holding this branch's name — nothing else can take it, including
    // the next run of this script.
    if (err instanceof WaitError) await ws.stop(err.run.id);
    throw err;
  }
}

const flightPrompt = `You are the flight search specialist. Read trip-brief.md.

Research realistic options from ${TRIP.origin} to ${TRIP.destination} for those dates,
preferring nonstop or one stop. Write flights.json:

{"search": {...}, "options": [{"id": "F1", "airline": "...", "price_usd": 850,
 "duration": "11h 20m", "stops": 0, "notes": "..."}], "recommended": "F1"}

Be realistic about 2026 prices. Write the file and stop.`;

const hotelPrompt = `You are the hotel search specialist. Read trip-brief.md.

Find three or four mid-range hotels in Tokyo near transit, $120-250 a night.
Write hotels.json:

{"city": "Tokyo", "check_in": "${TRIP.depart}", "check_out": "${TRIP.return}",
 "options": [{"id": "H1", "name": "...", "area": "...", "price_per_night_usd": 180,
 "total_estimate_usd": 1260, "rating": 4.5, "pros": ["..."], "cons": ["..."]}],
 "recommended": "H1"}

Write the file and stop.`;

// In parallel: two branches, two containers, two agents. allSettled rather than
// all, because one specialist failing is not a reason to lose the other's work —
// the coordinator is told what is missing instead.
const [flights, hotels] = await Promise.allSettled([
  specialist("agent-flights", flightPrompt),
  specialist("agent-hotels", hotelPrompt),
]);

for (const [what, r] of [["flights", flights], ["hotels", hotels]] as const) {
  if (r.status === "rejected") console.error(`${what}: ${r.reason}`);
  else console.log(`${what}: ${r.value.out.agent ?? AGENT} exited ${r.value.out.exitCode}`);
}

// The handover. Read each artifact out of the tree that produced it, and write
// it into the coordinator's — the step that makes this a workflow rather than
// three agents guessing in parallel.
const coord = await repo.workspace("agent-coordinator");
await coord.clearFinished();
await put(coord, "trip-brief.md", BRIEF);

const artifacts: string[] = [];
for (const [name, r] of [["flights.json", flights], ["hotels.json", hotels]] as const) {
  const content = r.status === "fulfilled" ? await get(r.value.ws, name) : "";
  if (content.trim() === "") continue;
  await put(coord, name, content);
  artifacts.push(name);
}
console.log(`handed over: ${artifacts.join(", ") || "nothing"}`);

const final = await coord.agent(
  AGENT,
  `You are the travel coordinator. You have trip-brief.md${
    artifacts.length ? ` and ${artifacts.join(" and ")}` : ""
  }.

${artifacts.length === 2
    ? "Pick the best flight and hotel combination that stays near the budget."
    : "Some specialist output is missing. Say so explicitly in the itinerary and work with what is here — do not invent the missing file's contents."}

Write itinerary.md for a human to read, and recommendation.json:

{"flight_id": "F1", "hotel_id": "H1", "estimated_total_usd": 2100,
 "summary": "...", "next_steps": ["..."]}

Any booking step is SIMULATED. Do not attempt a real payment or purchase.`,
  { timeoutMs: 10 * 60_000, fallback: ["codex"] },
);

console.log(`\ncoordinator exited ${final.exitCode}`);
console.log((await get(coord, "itinerary.md")).slice(0, 2000));

// The coordinator's verdict is the script's, so this can be the last line of a
// job without a wrapper deciding what "worked" meant.
process.exitCode = final.exitCode;
