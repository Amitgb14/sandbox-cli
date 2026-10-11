# Browser automation across N sandboxes

An example of the Python SDK: create N sandboxes from the desktop image, split
a list of URLs across them, drive each one's Chromium with Playwright, and
collect the results and screenshots. Every sandbox it made is terminated at
the end, also on failure or Ctrl-C, unless `--keep`.

```
fleet.py          the orchestrator (standard library + ../../sandboxapi)
job/job.js        what runs in each sandbox — edit visit() for your task
job/package.json  playwright-core only: it drives the image's /usr/bin/chromium, no browser download
urls.txt          input, one URL per line, split round-robin
results/<batch>/  results.json (merged), worker-NN/{results.json,NN.png,log.txt}
```

It needs the **desktop image** (`images/desktop`, docs/desktop.md): the base
image has no Chromium, and a sandbox has no root to install one.

## Running it on a Linux machine

The host needs Python 3.8+ only; Node and Chromium are in the image.

```sh
git clone … && cd sandbox-cli/sdk/python/examples/browser-fleet

# sandboxd on this machine: the socket sandbox-cli uses is found by default
python3 fleet.py -n 10

# another sandboxd or a gateway
SANDBOX_ENDPOINT=https://host:7443 SANDBOX_TOKEN=… python3 fleet.py -n 10

# a desktop image from your own registry
python3 fleet.py -n 10 --image registry.example/sandbox-desktop:edge
```

| Flag | Default | |
|---|---|---|
| `-n` | 10 | sandboxes (never more than there are URLs) |
| `--image` | `$SANDBOX_DESKTOP_IMAGE`, else a `sandbox-desktop` image the node already has, else `ghcr.io/amitgb14/sandbox-desktop:edge` | |
| `--memory` / `--cpus` | 2048 / 1 | Chromium is slow below 2 GiB; 10 sandboxes need ~20 GiB |
| `--network` | `open` | `allowlist`: only the URLs' hosts, `--allow HOST` and the npm registry |
| `--snapshot` | off | install once, start the rest from a snapshot (backends with `memory_snapshot` or `disk_snapshot`) |
| `--context` | `$SANDBOX_CONTEXT`, then the current one | which sandboxd, as `sandbox-cli context ls` names them; `--endpoint` names one directly |
| `--headed` | off | draw on the sandbox's desktop, to watch in Studio's Desktop tab |
| `--hold SECS` / `--slowmo MS` | 0 / 0 | with `--headed`: keep the browser open after the job / slow each action, to follow it |
| `--timeout` | 900 | seconds for one worker's job |
| `--keep` / `--cleanup BATCH` | | leave the sandboxes running / terminate a kept batch |

## Watching it in Studio

```sh
python3 fleet.py -n 2 --headed --hold 300 --slowmo 500
```

Then in Studio open each sandbox the script prints (`watch: Studio -> sbx_… ->
Desktop`) and its **Desktop** tab. Three things must hold, or there is
nothing to see:

- **The same sandboxd.** The script uses sandbox-cli's current context, as
  Studio does; the first line it prints names the endpoint. If they differ,
  pass `--context`.
- **Headed.** Without `--headed` the browser draws nowhere.
- **Still running.** Sandboxes are terminated when the job ends, and Studio
  lists live ones only: `--hold` keeps them up for a look, `--keep` leaves
  them until `--cleanup`.

Notes for a self-hosted Linux sandboxd:

- **Network.** An operator policy may refuse `open`; use `--network allowlist`.
  An allowlist names hosts, so a page's assets on other hosts (CDNs, fonts)
  do not load — add them with `--allow`, or the screenshots will show it.
- **Snapshots.** Where the backend takes them, `--snapshot` saves nine
  `npm install`s. On macOS it costs more than it saves: taking the disk
  snapshot takes over a minute, the install it replaces a few seconds.
- **Chromium's own sandbox** stays on where the guest kernel allows user
  namespaces and is dropped where it does not (the image's
  `/etc/chromium.d/sandbox`); the VM is the boundary either way.
