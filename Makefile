BINARY := sandbox-cli
STUDIO_API_BINARY := sandbox-studio-api
PKG := github.com/Amitgb14/sandbox-cli
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -X $(PKG)/internal/version.Version=$(VERSION)

.PHONY: build build-studio-api install test test-integration lint fmt clean snapshot release docker-build image

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/sandbox-cli

# The local HTTP control plane (internal/studioapi) — see docs/studio-api/.
build-studio-api:
	go build -ldflags "$(LDFLAGS)" -o bin/$(STUDIO_API_BINARY) ./cmd/sandbox-studio-api

# --- release engineering (GoReleaser) ----------------------------------------
# Install once: go install github.com/goreleaser/goreleaser/v2@latest

# Dry run: build the full matrix, archives, checksums and the Homebrew cask
# into ./dist without publishing anything.
snapshot:
	goreleaser release --snapshot --clean

# Publish. Normally you don't run this by hand — pushing a tag triggers
# .github/workflows/release.yml, which runs the same command in CI. Doing it
# locally needs GITHUB_TOKEN (repo + tap write access) and a pushed tag:
#   git tag 0.0.1beta.1 && git push origin 0.0.1beta.1 && make release
release:
	@command -v goreleaser >/dev/null || { echo "error: goreleaser required: go install github.com/goreleaser/goreleaser/v2@latest"; exit 1; }
	goreleaser release --clean --skip=validate

# One binary for this machine, built in Docker. -> bin/sandbox-cli
docker-build:
	docker build --target export --build-arg VERSION=$(VERSION) \
	  --output type=local,dest=./bin .

# Multi-arch runnable image (requires buildx).
image:
	docker buildx build --target runtime --build-arg VERSION=$(VERSION) \
	  --platform linux/amd64,linux/arm64 -t $(BINARY):$(VERSION) .

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/sandbox-cli

test:
	go test ./...

# Requires a running Docker daemon; builds the base image on first run.
test-integration:
	go test -tags docker_integration -count=1 ./...

fmt:
	gofmt -w .

clean:
	rm -rf bin dist bin-docker
