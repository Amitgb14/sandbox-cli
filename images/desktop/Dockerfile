# The desktop image: the base image, plus a screen to look at. A sandbox made
# from it runs `sandbox-desktop`, which starts a virtual X display, a window
# manager, a terminal and a browser, and serves the display over VNC on the
# guest's own loopback (port 5900). Studio's Desktop tab reaches that port
# through the API's tunnel and draws it with its own VNC client, so nothing the
# guest serves is ever rendered as a page: only pixels and input cross.
#
# Built in CI only, on top of the base image of the same build.
ARG BASE=ghcr.io/amitgb14/sandbox-base:edge
FROM ${BASE}

RUN apt-get update && apt-get install -y --no-install-recommends \
      xvfb \
      x11vnc \
      openbox \
      xterm \
      chromium \
      fonts-dejavu-core \
      fonts-noto-color-emoji \
      x11-utils \
      dbus-x11 \
    && rm -rf /var/lib/apt/lists/*

COPY sandbox-desktop /usr/local/bin/sandbox-desktop
COPY menu.xml /etc/xdg/openbox/menu.xml
COPY chromium-flags /etc/chromium.d/sandbox
RUN chmod 0755 /usr/local/bin/sandbox-desktop \
    # X keeps its socket here; created ahead so the sandbox user, not root,
    # can start the display.
    && mkdir -p -m 1777 /tmp/.X11-unix
