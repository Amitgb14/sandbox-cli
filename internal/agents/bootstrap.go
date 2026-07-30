package agents

// The container-side install scripts, here rather than in internal/cli because
// the fleet needs the same argv the interactive wrapper uses. They started in
// the wrappers, which was fine while only claude and codex could be launched
// headlessly; the moment a fleet task could name `gemini`, a second copy of this
// script would have had to exist to build that task's Command — and the package
// doc above says what two copies of a security-relevant script do.
//
// internal/cli keeps one-line aliases (agentBootstrap / npmAgentBootstrap) so
// the fifteen wrappers read as they did.

// Bootstrap builds the container argv for an agent that the base image may not
// carry. It prefers whatever is already on PATH — the baked copy, for the agents
// the image does carry — and otherwise runs install once, into the persisted
// HOME (~/.local, already ahead on PATH), where it stays for every later run of
// that agent.
//
// Installing lazily instead of baking is what keeps the base image from growing
// with each adapter added. Baking every agent would put hundreds of megabytes in
// front of every user on first build, most of it for agents they will never run;
// this way an unused adapter costs nothing but the few lines of Go, and the
// agents you do use are downloaded once into a directory that outlives the
// container.
//
// The two costs are real and are why the install is announced rather than
// silent: the first run of an agent waits for a download, and it needs network
// at that moment — under `--allow` the npm registry is in the baseline, but a
// vendor's own download host may not be.
//
// The trailing bin is sh's argv[0] for the script, so "$@" is exactly the guest
// args appended by the caller.
func Bootstrap(bin, install string) []string {
	// $HOME/.local/bin is APPENDED, not prepended, and the agent is exec'd from it
	// by absolute path.
	//
	// That directory is the persisted agent HOME: bind-mounted from the host, the
	// same one in every project, and writable by the agent. Prepending it put it
	// ahead of /usr/bin for every future session in every project — so an agent
	// compromised in one repository could drop a file named `git`, `node` or `sh`
	// there and shadow that command everywhere afterwards. Appending keeps the
	// directory usable while system binaries win, and the absolute exec below is
	// what still lets the agent self-update, which is the reason it lives there.
	script := `export PATH="$PATH:$HOME/.local/bin"
if [ -x "$HOME/.local/bin/` + bin + `" ]; then
  exec "$HOME/.local/bin/` + bin + `" "$@"
fi
if ! command -v ` + bin + ` >/dev/null 2>&1; then
  echo "sandbox-cli: installing ` + bin + ` into the sandbox agent home (first run only)..." >&2
  ` + install + ` >/dev/null 2>&1 || true
fi
if ! command -v ` + bin + ` >/dev/null 2>&1; then
  echo "sandbox-cli: ` + bin + ` is not installed, and installing it just now failed." >&2
  echo "sandbox-cli: the sandbox needs network access on an agent's first run." >&2
  echo "sandbox-cli: with --allow, the install host must be on the allowlist." >&2
  exit 127
fi
exec ` + bin + ` "$@"`
	return []string{"sh", "-c", script, bin}
}

// NpmBootstrap is Bootstrap for an agent distributed as an npm package.
// --prefix keeps the install inside the persisted HOME, which is the only
// writable place that survives the container.
func NpmBootstrap(bin, pkg string) []string {
	return Bootstrap(bin, `npm install -g --prefix "$HOME/.local" `+pkg)
}
