# A desktop in a sandbox

A sandbox made from the desktop image has a screen: a window manager, a
terminal and Chromium, which you see and use in Studio's **Desktop** tab. It is
for what a terminal cannot show — a page an agent is building, a site to click
through by hand, a program with a window — and for watching an agent that
drives a browser.

## Making one

```sh
sandbox-cli run --keep --name desktop \
  --image ghcr.io/amitgb14/sandbox-desktop:edge --memory 2048 -- true
```

Then open it in Studio (`sandbox-cli studio`), choose the **Desktop** tab and
**Start desktop**. The screen appears a second or two later. Closing the tab
leaves the desktop running; opening it again joins it where it was, and two
viewers may watch at once.

Give it 2 GiB or more: a browser in 1 GiB is slow, and runs out of memory on a
heavy page. A browser that should reach the web needs a network that lets it
(`--network open`, or an allowlist naming the sites).

The image is published with the base image, under the same tags:
`sandbox-desktop:edge` from `main`, and `:<version>` and `:latest` from a
release. It is the base image plus a screen, so everything in the base image
— the agents, git, the toolchains — is there too.

## How it works

```
Studio tab (noVNC, Studio's own code)
   │  WebSocket /api/ws/desktop, binary frames
Studio's server (sandbox-cli studio)
   │  the API's tunnel, to port 5900 only
sandboxd ─── the guest's own loopback ─── x11vnc ─── Xvfb :1 ─── openbox, xterm, chromium
```

`sandbox-desktop`, in the image, starts a virtual display, the window manager,
a terminal and the browser, and serves the display over VNC on the guest's
loopback, port 5900, with no password. Started from Studio, it is an ordinary
background process of the sandbox: it is in the process list, its output is
in Logs, and stopping it (or the sandbox) stops the desktop.

Studio reaches that port the way `sandbox-cli tunnel` does, through the API,
and draws the screen itself:

- **Only pixels and input cross.** Studio's VNC client is part of Studio; the
  guest sends the screen and receives keys and pointer moves. Nothing the
  sandbox serves is shown as a web page on Studio's address, where it could
  run beside Studio's token — which is why this is not a general port
  forward in the browser.
- **One port.** Studio's server bridges the desktop's port and no other,
  whatever a page asks for. It takes Studio's token and refuses another
  origin, like every Studio call.
- **No new way in.** The VNC server listens on the guest's loopback, which only
  the API's tunnel reaches — so only a caller allowed to use the sandbox.
  X11 forwarding stays refused: the guest never draws on your machine's
  display.

Chromium keeps its own sandbox where the guest kernel lets the sandbox user
make a user namespace (the macOS backend's kernel does), as a second wall
inside the VM. Where it does not, it runs without it rather than not at all;
the VM is the boundary.

## Settings

`sandbox-desktop` reads two variables, set like any other for the process:

| Variable | Default | |
|---|---|---|
| `SANDBOX_DESKTOP_SIZE` | `1280x800x24` | the screen: width x height x depth |
| `SANDBOX_DESKTOP_PORT` | `5900` | the VNC port; Studio's tab reaches 5900 only |

## Limits

- **It keeps the sandbox up.** A running process counts as activity, so a
  sandbox with its desktop running never reaches its idle timeout. Stop the
  desktop, or terminate the sandbox, when you are done.
- **Fonts.** The image carries Latin, emoji and the common DejaVu faces. Text in
  other scripts (CJK among them) shows as boxes until a font for it is
  installed in the sandbox.
- **No sound and no clipboard** between the desktop and your machine.
- Linux only, as every sandbox is.
