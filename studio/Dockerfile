# syntax=docker/dockerfile:1.7
#
# The Studio UI, published so it can be pulled rather than built.
#
# This is the *production* image — `next build` compiled ahead of time and
# served by node. Dockerfile.dev beside it is the other one: `next dev` against
# source bind-mounted from the host, which is what `docker compose up` uses so
# that editing a file changes what you see. Neither replaces the other.
#
# It serves a UI and nothing else. No docker socket, no host mounts, no project
# — the browser talks to the daemon directly, and every screen in here is a view
# of what that daemon answers. That is why this half is safe to hand somebody as
# an image to pull, and the API half is not (see docker-compose.yml).
#
# ── Why this builds per-architecture, slowly, on purpose ─────────────────────
#
# The tempting version pins the build stage to $BUILDPLATFORM: `next build`
# emits JavaScript, the same bytes whatever machine produced it, so compiling
# once natively and varying only the runtime layer makes arm64 nearly free
# instead of a QEMU-paced twenty minutes.
#
# It is wrong, and quietly. `output: "standalone"` does not emit JavaScript
# alone — it traces the server's dependencies and copies them in, *as installed
# on the builder*. This image ends up carrying
# `node_modules/@img/sharp-linux-<arch>/lib/sharp-linux-<arch>.node`, a native
# binary Next uses for image optimization. Cross-built that way, the arm64 image
# would ship amd64 objects: fine until something loads them, and the CI pull
# request build only covers amd64, so the first machine to find out would be a
# user's Mac pulling `latest`.
#
# A published artifact is the wrong place to trade correctness for build
# minutes, and this cost lands only on tag and main pushes — pull requests build
# one native architecture and push nothing.

FROM node:22-bookworm-slim AS build
WORKDIR /app

ENV NEXT_TELEMETRY_DISABLED=1

# Dependencies first, so a source-only change reuses this layer.
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY . .
RUN npm run build

FROM node:22-bookworm-slim AS runtime
WORKDIR /app

ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3100 \
    HOSTNAME=0.0.0.0

# The three pieces `output: "standalone"` produces. `static` and the traced
# server are separate on purpose — Next copies neither into the other, and a
# missing `.next/static` is a page that renders unstyled rather than an error
# that says so.
COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static

# node:22 ships this user; nothing here is written at runtime, so the server has
# no reason to be root.
USER node

EXPOSE 3100

# HOSTNAME=0.0.0.0 binds every interface *inside this container*, which is what
# a published port needs. The boundary is the publish spec on the host side —
# studio.sh binds it to 127.0.0.1 — not this line.
CMD ["node", "server.js"]
