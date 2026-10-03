# The base image every sandbox starts from, unless a request names another.
#
# Built in CI only — users never need a container engine. CI publishes it as an
# OCI image; on Linux, sandboxd turns it into a read-only ext4 disk
# (internal/image) and boots it under Firecracker, and on macOS the native
# `container` runtime runs it directly.
#
# What is NOT here any more, and why: the beta.15 image carried an egress
# firewall, a name-matching proxy and a privilege-dropping entrypoint, all run
# as root inside the container. Egress is now enforced on the host, outside
# anything the guest can touch, and the guest agent drops to the sandbox user
# itself — so the image needs no root phase at all.

# The guest agent, built from this repository, static.
FROM golang:1.25-bookworm AS guestd
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/sandbox-guestd ./cmd/sandbox-guestd
COPY internal ./internal
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/Amitgb14/sandbox-cli/internal/version.Version=${VERSION}" \
      -o /sandbox-guestd ./cmd/sandbox-guestd

FROM node:22-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
      git \
      curl \
      ca-certificates \
      ripgrep \
      less \
      openssh-client \
      bash \
      jq \
      python3 \
      build-essential \
      iproute2 \
      procps \
      # Goose's installer extracts a .tar.bz2 and GNU tar shells out to bzip2.
      bzip2 \
    && rm -rf /var/lib/apt/lists/*

# The agents with a verified headless mode are baked in, so a sandbox can run
# one with no network beyond its provider. One package per layer: a single
# unavailable package must not take the others down with it. The other adapters
# install into the persisted HOME on first use.
RUN npm install -g @anthropic-ai/claude-code || true
RUN npm install -g @openai/codex || true
RUN npm install -g @google/gemini-cli || true
RUN npm install -g opencode-ai || true

# The sandbox user. uid and gid are fixed at 1001 because the guest agent runs
# processes by number (it does not consult /etc/passwd), and on macOS the bind
# mount ownership model is keyed on the same number. The node image already
# uses 1000.
RUN groupadd -g 1001 sandbox \
    && useradd -m -u 1001 -g 1001 -d /sandbox/home -s /bin/bash sandbox \
    && mkdir -p /workspace \
       /sandbox/home/.npm /sandbox/home/.cache/pip /sandbox/home/go/pkg/mod \
    && chown -R 1001:1001 /sandbox/home /workspace

# Present in the image for the macOS backend, which runs
# `sandbox-guestd serve --stdio` per request. On Linux, BuildRootFS injects the
# agent matching the running sandboxd, so the two can never disagree.
COPY --from=guestd /sandbox-guestd /sbin/sandbox-guestd

ENV HOME=/sandbox/home
WORKDIR /workspace
# No ENTRYPOINT and no USER: on Linux the guest agent is PID 1 (named on the
# kernel command line) and drops to uid 1001 for every process it starts; on
# macOS the backend names the command. The image decides neither.
