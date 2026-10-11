#!/usr/bin/env python3
"""Run a browser automation job across N sandboxes in parallel.

Each sandbox is made from the desktop image (Chromium plus its libraries),
gets ./job uploaded, installs playwright-core (no browser download), runs
`node job.js` on its share of the URLs, and hands back results.json and the
screenshots. Every sandbox it made is terminated at the end, also on failure
or Ctrl-C, unless --keep.

    python3 fleet.py -n 10                       # 10 sandboxes, urls.txt
    python3 fleet.py -n 10 --snapshot            # install once, start 10 from a snapshot
    python3 fleet.py -n 2 --headed --hold 300    # watch it in Studio's Desktop tab
    python3 fleet.py --cleanup BATCH             # terminate a kept batch
    python3 fleet.py --network allowlist         # only the URLs' hosts and the npm registry

Endpoint: the same sandboxd sandbox-cli (and so Studio) uses — --context, then
$SANDBOX_CONTEXT, then the current context (sandbox-cli context ls), with its
token, CA and organization. --endpoint or $SANDBOX_ENDPOINT (with
$SANDBOX_TOKEN) names one directly instead. The SDK is the one in
this repository (sdk/python), or $SANDBOX_SDK.
"""
import argparse
import concurrent.futures as cf
import json
import os
import sys
import time
import urllib.parse
import uuid
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, os.environ.get("SANDBOX_SDK", str(HERE.parents[1])))
from sandboxapi import ApiError, Client  # noqa: E402

# Where sandboxd listens by default, as sandbox-cli finds it (internal/cli's
# localSocket): $XDG_RUNTIME_DIR on Linux, the config directory on macOS.
CONFIG_DIR = Path(os.environ.get("XDG_CONFIG_HOME") or Path.home() / ".config") / "sandbox"
_SOCKET_DIR = os.environ.get("XDG_RUNTIME_DIR") or str(CONFIG_DIR)
DEFAULT_ENDPOINT = "unix://" + os.path.join(_SOCKET_DIR, "sandboxd.sock")
# npm install needs the registry; an allowlist adds it to the URLs' hosts.
NPM_REGISTRY = "registry.npmjs.org"
# The published desktop image. A desktop image the node already has (a local
# build, say) is preferred to it: see pick_image.
DEFAULT_IMAGE = "ghcr.io/amitgb14/sandbox-desktop:edge"
JOB_DIR = "/sandbox/home/job"


def resolve_endpoint(args):
    """The endpoint, token, CA and org to use, picked the way sandbox-cli picks
    them (internal/cli/contexts.go), so a batch lands where Studio looks."""
    if args.endpoint:
        return {"endpoint": args.endpoint, "token": os.environ.get("SANDBOX_TOKEN", "")}
    try:
        cf = json.loads((CONFIG_DIR / "contexts.json").read_text())
    except FileNotFoundError:
        cf = {}
    name = args.context or os.environ.get("SANDBOX_CONTEXT") or cf.get("current") or "local"
    ctx = (cf.get("contexts") or {}).get(name)
    if ctx is None:
        if name != "local":
            raise SystemExit(f"no context {name!r} (sandbox-cli context ls)")
        ctx = {"endpoint": DEFAULT_ENDPOINT}
    token = os.environ.get("SANDBOX_TOKEN", "")
    if ctx.get("token_file"):
        token = Path(ctx["token_file"]).expanduser().read_text().strip()
    return {"name": name, "endpoint": ctx["endpoint"], "token": token,
            "ca_file": ctx.get("ca_file") or None, "org": ctx.get("org", "")}


def client(args) -> Client:
    e = args.ep
    return Client(e["endpoint"], token=e["token"], ca_file=e.get("ca_file"), org=e.get("org", ""),
                  timeout=args.timeout + args.hold + 120)


def say(worker, msg):
    print(f"[{time.strftime('%H:%M:%S')}] [{worker:>4}] {msg}", flush=True)


def check(res, what):
    if res["exit_code"] != 0 or res.get("timed_out"):
        tail = (res["stderr"] or res["stdout"]).decode(errors="replace")[-2000:]
        raise RuntimeError(f"{what} failed (exit {res['exit_code']}, timed_out={res.get('timed_out')}):\n{tail}")
    return res


def upload_job(c, sbx):
    for f in ("package.json", "job.js"):
        c.write_file(sbx, f"{JOB_DIR}/{f}", (HERE / "job" / f).read_bytes())


def install(c, sbx):
    check(c.run(sbx, ["npm", "install", "--no-audit", "--no-fund", "--loglevel=error"],
                cwd=JOB_DIR, timeout_secs=600), "npm install")


def start_desktop(c, sbx):
    """Xvfb + window manager + VNC on :1, so a headed browser is visible in Studio."""
    c.start_process(sbx, ["sandbox-desktop"])
    for _ in range(50):
        if c.run(sbx, ["xdpyinfo"], env={"DISPLAY": ":1"})["exit_code"] == 0:
            return
        time.sleep(0.2)
    raise RuntimeError("the desktop did not start")


def create(c, args, batch, name, snapshot_id=""):
    return c.create_sandbox(
        name=name, image="" if snapshot_id else args.image, snapshot_id=snapshot_id,
        cpus=args.cpus, memory_mb=args.memory, network=args.net,
        idle_timeout_secs=args.idle, labels={"batch": batch, "purpose": "browser-fleet"},
    )["id"]


def pick_image(c):
    """A sandbox-desktop image the node has already built, so nothing is pulled;
    else the published one. A gateway has no /v1/node, so it gets the latter."""
    try:
        have = c._json("GET", "/v1/node").get("images") or []
    except (ApiError, OSError):
        have = []
    local = sorted((i for i in have if "sandbox-desktop" in i), key=lambda i: i != DEFAULT_IMAGE)
    return local[0] if local else DEFAULT_IMAGE


def prepare_snapshot(args, batch):
    """One sandbox installs the job; the rest start from its snapshot."""
    c = client(args)
    caps = c.capabilities()["capabilities"]
    if not (caps.get("memory_snapshot") or caps.get("disk_snapshot")):
        raise SystemExit("this endpoint takes no snapshots; run without --snapshot")
    sbx = create(c, args, batch, f"bf-{batch}-template")
    try:
        say("tmpl", f"sandbox {sbx}: installing")
        upload_job(c, sbx)
        install(c, sbx)
        snap = c.create_snapshot(sbx)["id"]
        say("tmpl", f"snapshot {snap}")
        return snap
    finally:
        c.terminate_sandbox(sbx)


def worker(args, batch, i, urls, snapshot_id, out_root):
    c = client(args)
    sbx = None
    try:
        sbx = create(c, args, batch, f"bf-{batch}-{i}", snapshot_id)
        say(i, f"sandbox {sbx} ({len(urls)} urls)")
        upload_job(c, sbx)  # again from a snapshot too: job.js may have changed since
        if not snapshot_id:
            install(c, sbx)
        if args.headed:
            start_desktop(c, sbx)
        c.write_file(sbx, f"{JOB_DIR}/urls.json", json.dumps(urls).encode())
        if args.headed:
            say(i, f"watch: Studio -> {sbx} -> Desktop"
                   + (f" (stays open {args.hold}s after the job; Ctrl-C ends it)" if args.hold else ""))
        res = c.run(sbx, ["node", "job.js"], cwd=JOB_DIR, timeout_secs=args.timeout + args.hold,
                    env={"WORKER": str(i), "HEADED": "1" if args.headed else "0",
                         "HOLD": str(args.hold), "SLOWMO": str(args.slowmo)})
        out = out_root / f"worker-{i:02d}"
        out.mkdir(parents=True, exist_ok=True)
        (out / "log.txt").write_bytes(res["stdout"] + res["stderr"])
        try:
            for e in c.list_dir(sbx, f"{JOB_DIR}/out"):
                if e["type"] == "file":
                    (out / e["name"]).write_bytes(c.read_file(sbx, f"{JOB_DIR}/out/{e['name']}"))
        except ApiError as e:
            say(i, f"no output to fetch: {e}")
        say(i, f"exit {res['exit_code']}{' (timed out)' if res.get('timed_out') else ''} -> {out}")
        return {"worker": i, "sandbox": sbx, "exit_code": res["exit_code"], "timed_out": res.get("timed_out")}
    except Exception as e:  # one worker's failure must not stop the others
        hint = ""
        if sbx is None and isinstance(e, ApiError):
            hint = f" (could the node not get image {args.image}? pass --image)"
        say(i, f"FAILED: {e}{hint}")
        return {"worker": i, "sandbox": sbx, "error": str(e)}
    finally:
        if sbx and not args.keep:
            try:
                c.terminate_sandbox(sbx)
            except ApiError:
                pass


def cleanup(args, batch):
    c = client(args)
    for s in c.sandboxes(labels={"batch": batch}):
        if s.get("state") == "terminated":  # the listing keeps terminated ones
            continue
        say("-", f"terminating {s['id']}")
        c.terminate_sandbox(s["id"])


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("-n", "--sandboxes", type=int, default=10)
    p.add_argument("--urls", default=str(HERE / "urls.txt"))
    p.add_argument("--image", default=os.environ.get("SANDBOX_DESKTOP_IMAGE", ""),
                   help="default: a desktop image the node already has, else " + DEFAULT_IMAGE)
    p.add_argument("--context", help="a sandbox-cli context (default: $SANDBOX_CONTEXT, then the current one)")
    p.add_argument("--endpoint", default=os.environ.get("SANDBOX_ENDPOINT", ""),
                   help="an endpoint directly, instead of a context ($SANDBOX_TOKEN for its token)")
    p.add_argument("--cpus", type=float, default=1)
    p.add_argument("--memory", type=int, default=2048, help="MiB; Chromium is slow below 2 GiB")
    p.add_argument("--network", default="open", choices=("open", "allowlist"),
                   help="allowlist: only the URLs' hosts, --allow and the npm registry (Linux backends)")
    p.add_argument("--allow", action="append", default=[], metavar="HOST",
                   help="with --network allowlist, also allow this host (repeatable)")
    p.add_argument("--timeout", type=int, default=900, help="seconds for one worker's job")
    p.add_argument("--idle", type=int, default=1800, help="idle timeout, a backstop if this script dies")
    p.add_argument("--snapshot", action="store_true", help="install once, start the rest from a snapshot")
    p.add_argument("--headed", action="store_true", help="draw on the desktop (watch in Studio)")
    p.add_argument("--hold", type=int, default=0, metavar="SECS",
                   help="keep the browser open this long after the job, to look at it (with --headed)")
    p.add_argument("--slowmo", type=int, default=0, metavar="MS",
                   help="slow every browser action down by this much, to follow it (with --headed)")
    p.add_argument("--keep", action="store_true", help="leave the sandboxes running")
    p.add_argument("--out", default=str(HERE / "results"))
    p.add_argument("--cleanup", metavar="BATCH", help="terminate a batch left by --keep, and exit")
    args = p.parse_args()
    args.ep = resolve_endpoint(args)
    if not args.image:
        args.image = pick_image(client(args))

    if args.cleanup:
        return cleanup(args, args.cleanup)

    urls = [l.strip() for l in Path(args.urls).read_text().splitlines() if l.strip() and not l.startswith("#")]
    args.net = {"mode": args.network}
    if args.network == "allowlist":
        hosts = {urllib.parse.urlsplit(u).hostname for u in urls} | set(args.allow) | {NPM_REGISTRY}
        args.net["allow"] = sorted(h for h in hosts if h)
    n = min(args.sandboxes, len(urls)) or 1
    shares = [urls[i::n] for i in range(n)]
    batch = time.strftime("%m%d%H%M") + "-" + uuid.uuid4().hex[:4]
    out_root = Path(args.out) / batch
    say("-", f"batch {batch}: {len(urls)} urls over {n} sandboxes, image {args.image}, "
             f"endpoint {args.ep.get('name', '')} {args.ep['endpoint']}")

    snap = None
    t0 = time.time()
    try:
        if args.snapshot:
            say("-", "building the snapshot: one install, then a snapshot (minutes on macOS)")
            snap = prepare_snapshot(args, batch)
        with cf.ThreadPoolExecutor(max_workers=n) as pool:
            runs = list(pool.map(lambda i: worker(args, batch, i, shares[i], snap, out_root), range(n)))
    except KeyboardInterrupt:
        say("-", "interrupted; terminating the batch")
        if not args.keep:
            cleanup(args, batch)
        raise
    finally:
        if snap:
            try:
                client(args).delete_snapshot(snap)
            except ApiError:
                pass

    # Merge every worker's results into one file.
    merged = []
    for f in sorted(out_root.glob("worker-*/results.json")):
        merged += [dict(r, worker=f.parent.name) for r in json.loads(f.read_text())]
    out_root.mkdir(parents=True, exist_ok=True)
    (out_root / "results.json").write_text(json.dumps({"runs": runs, "results": merged}, indent=2))
    ok = sum(r.get("ok", False) for r in merged)
    say("-", f"done in {time.time() - t0:.0f}s: {ok}/{len(urls)} urls ok, "
             f"{sum('error' in r for r in runs)} worker(s) failed -> {out_root / 'results.json'}")
    if args.keep:
        say("-", f"kept; terminate with: python3 {sys.argv[0]} --cleanup {batch}")
    return 0 if ok == len(urls) else 1


if __name__ == "__main__":
    sys.exit(main())
