# beta.15's invariants in the new tree

The plan's condition for deleting `_old/` is that every invariant in
`_old/CLAUDE.md` either has a test in the new tree or a note saying the
backend change made it obsolete. This is that record. It was built on
2026-10-02 by reading all of `_old/CLAUDE.md`, and statuses are as of the
commits that followed.

What the check found mattered more than the table. Five rules failed open in
the new tree, and each is now fixed with a test that fails on the code before
it:

| Found | Fixed by |
|---|---|
| An allowlist that resolved to nothing sent no network, so the server's default (`github.com` in it) applied. `--allow` ignored `baseline: false`, and `--network` skipped the profile | `resolveNetwork`, `TestNetworkFollowsTheConfigAndTheProfile` |
| A fleet built its own request, ignoring the user's config and profile | `Runner.Prepare` → `applyConfig`, `TestFleetTasksTakeTheConfigAndProfile` |
| sandboxd served loopback TCP with no token, so any local user could reach the API and type into an agent's terminal | `openListener`, `TestOpenListenerRefusesAnUnguardedPort` |
| Ctrl-C while watching a detached run killed the run | `attach(…, forward)`, `TestInterruptDetachesAWatcherAndStopsAForegroundRun` |
| The client's merged config was never validated | `loadConfig` → `Config.Validate`, `TestLoadConfigValidatesTheMergedConfig` |

Two more turned up beside the check:

- **Saved logins could carry an MCP server between repositories.** Fixed by
  `agents.FilterAuth`, tested by `TestSavedLoginsCarryNoCommands`.
- **The audit log kept every argv, prompts included.** Fixed: it now records
  the program, argument count and a hash. Tested by the conformance test
  `TheAuditLogNeverKeepsAProcesssArguments`.

The timezone resolver had been ported and never called. It is now wired up in
the client (`TestRunsCarryTheClientsZone`).

**Statuses**

- **TESTED:** a test asserts the rule. The test is named.
- **OBSOLETE:** the mechanism is gone. The reason is stated in the new
  design's terms.
- **CODE-ONLY:** the code keeps the rule and no test pins it. Each such row
  says why that is acceptable.
- **HANDED OVER:** only a real host can show it. The row names its
  end-to-end row.

| # | Invariant | Status | Where / why |
|---|---|---|---|
| 1 | Only the project reaches the sandbox; the host home is never mounted | TESTED | `TestBuildConfigGolden`, `TestBuildConfigWithVolumesGolden`, `TestBuildRunArgsGolden`; the workspace is a bundle (`testWorkspaceBundle`) |
| 2 | Config precedence: default → user → project → flags | TESTED | `TestLoad_ProjectOverridesDefault`, `TestLoadAppliesTheProfileAsABaseLayer` |
| 3 | Mount paths resolve relative to their file | OBSOLETE | `mounts` is a dead key, refused by `CheckLiveKeys` (`TestCheckLiveKeys`) |
| 4 | Never `/`, home or an ancestor, by device+inode | TESTED | `hostpath` tests; `TestBindIsRefusedUnlessAllowedAndSafe` |
| 5 | Host TZ forwarded as a name; the user's TZ wins; unknown sends nothing | TESTED | `TestRunsCarryTheClientsZone`, `TestValidZoneName` (moved to the client) |
| 6 | Renderers are pure; goldens are updated deliberately | TESTED | the golden tests above, `TestRulesetGolden` |
| 7 | A dash-leading image cannot become a flag | TESTED | `TestResolveRefusesLimitsAndBadValues`; `--` in macOS args |
| 8 | Podman dialect | OBSOLETE | no container engine |
| 9 | Sandboxes cannot reach each other | TESTED (Firecracker) / HANDED OVER | `TestRulesetGolden` drops `sbx*`↔`sbx*`; conformance `SandboxesCannotReachEachOther`, end-to-end row 31 (macOS open mode) |
| 10 | userns keep-id, uid mapping, SELinux relabel | OBSOLETE | the guest owns its disk; macOS `--bind` ownership is end-to-end row 10 |
| 11 | Shared group and umask | OBSOLETE | work returns as bundles |
| 12 | `sandbox-init` entrypoint | OBSOLETE | guestd is PID 1 or exec'd by the backend |
| 13 | Workspace writability pre-check | OBSOLETE | the workspace is a clone in the guest |
| 14 | Worktrees by branch | OBSOLETE | fleet tasks are sandboxes; work lands from refs |
| 15 | `network: none` renders no network | TESTED | `vmconfig-no-network.json`, macOS args |
| 16 | An allowlist that cannot be enforced refuses | TESTED | `TestBuildRunArgsRefusesAnAllowlist`, `testNetworkUpdate`, `TestVMEgress` |
| 17 | `baseline: false` drops the built-ins, across layers and flags | TESTED | `TestEgressDomains_BaselineOff`, `TestLoad_BaselineIsTriStateAcrossLayers`, `TestNetworkFollowsTheConfigAndTheProfile` |
| 18 | An allowlist that resolves to nothing refuses | TESTED | `TestNetworkTightenNeverLoosen`, `testNetworkEmptyAllowlist`, `TestNetworkFollowsTheConfigAndTheProfile` |
| 19 | Egress by name; subdomains not implied | TESTED | `egressproxy` host, server and dns tests |
| 20 | Ingress default-deny | TESTED | `TestRulesetGolden`; tunnels instead of ports (`testTunnel`) |
| 21 | The proxy does not terminate TLS | CODE-ONLY | `egressproxy/server.go` tunnels after reading SNI. `TestAllowedHostIsTunnelled` checks the tunnel, not that the bytes are unmodified. Low impact: a proxy that altered TLS would break the handshake visibly |
| 22 | Firewall/proxy inside the container | OBSOLETE | both run on the host, outside the VM |
| 23 | A checkpoint never touches the agent's index, HEAD or branches | TESTED | `TestCheckpointLeavesTheAgentsStateAlone` |
| 24 | Guest bundles verify, carry one ref, land only in `refs/sandbox/` | TESTED | `TestBundlesFromTheGuestAreCheckedBeforeFetching`, `TestBringBackRefusesReservedNames` |
| 25 | Recover says where the work is, without guessing | TESTED | `TestRecoverSaysWhereTheWorkIs` |
| 26 | Host transcript store rules | OBSOLETE | transcripts are read from the guest |
| 27 | A user turn is a typed prompt; unverified formats are not read | TESTED | `TestParseTranscript`, `TestTranscriptRefusesASymlink`, `TestReadTranscriptFromTheSandbox` |
| 28 | Usage reading | OBSOLETE | `usage` was dropped |
| 29 | Audit keeps env names, never values; never fails a request | TESTED | `TestRecordWritesNamesNotValues`, `TestLogNeverFailsARequest`, `testAudit`, `testEnvValuesHidden`, `TheAuditLogNeverKeepsAProcesssArguments` |
| 30 | Only verified headless agents run unattended | TESTED | `TestEveryAgentHasAVerifiedHeadlessArgv`, `TestLaunchValidates` |
| 31 | One bootstrap; agent-writable dirs appended to PATH | TESTED | `TestBootstrapAppendsToPath`, `TestBootstrapUsesAReadyToolsVolume` |
| 32 | A descriptor produces no host paths | OBSOLETE | `AuthPaths` are guest paths |
| 33 | Every gate on the run path holds for every caller | TESTED | fleet and Studio go through `applyConfig`: `TestFleetTasksTakeTheConfigAndProfile`; Studio launches through `runSandbox` |
| 34 | Verify runs in the guest; its exit is the answer | TESTED | `TestWithVerify*` |
| 35 | fleet.yaml has flag trust; unknown keys fail | TESTED | `TestLoadRejectsUnknownKeys`, `TestCacheIsRefusedNotIgnored` |
| 36 | Per-task caps replace, per-task allow adds | TESTED | `TestLimitsFor`; the allow goes through `resolveNetwork` |
| 37 | `max_parallel` and host capacity | OBSOLETE | an in-process semaphore; the server's limits |
| 38 | `land --all`: a branch refusal skips, a base refusal stops | TESTED | `TestLandAllSkipsBranchesAndStopsOnTheBase`, `TestLandRefusesWhatIsNotReady` |
| 39 | The recorded base ≠ HEAD refuses unless `--onto` | TESTED | same |
| 40 | The worktree is still on its branch | OBSOLETE | land merges a ref |
| 41 | Fail over only when nothing changed | TESTED | `TestRoutedRun*`, `TestShouldFailOver` |
| 42 | 401/403/404/429 mean up; unprobed is not down | TESTED | `TestProbeClassifiesResponses`, `TestProbeSkipsAgentsWithNoProviderHost` |
| 43 | The probe carries no credential | TESTED | `TestProbeCarriesNoCredential` |
| 44 | Retarget via the prompt; refuse a trailing flag | TESTED | `TestRoutedRunRefusals`, `TestResolve` |
| 45 | Handoff is a briefing, not a resume | TESTED | `handoff` tests |
| 46 | Every switch recorded with one route id | TESTED | `TestRoutedRunFallsThroughOnlyWhenNothingChanged`, `TestBuildLabels` |
| 47 | `routing:`/`providers:` refused from a project | TESTED | `TestProjectConfigRefusesPrivilegedKeys` |
| 48 | Detached routing via a supervisor | OBSOLETE | refused instead (`TestRoutedRunRefusals`) |
| 49 | githard overrides beat local config, across scopes | TESTED | `TestArgsNeutralises*`, `TestArgsDisablesSigningPrograms`, `TestSnapshotConfigSeesWorktreeScope` |
| 50 | Hooks, fsmonitor, filters, textconv neutralised; every host git through githard | TESTED | `TestHardenedGitRunsNoProgramTheRepositoryNames`, `TestEveryHostGitCallIsHardened` |
| 51 | The user's own `worktree git` exception | OBSOLETE | the command is gone |
| 52 | Secret values never in an argv | TESTED | `TestBuildConfigCarriesNoEnvironmentValue`, `TestBuildRunArgsCarryNoEnvironmentValue`, `testEnvValuesHidden` |
| 53 | Secrets cannot steer sandbox-cli's children | OBSOLETE | values never enter the client's environment |
| 54 | Lifetime warning rules | TESTED | `creds` lifetime tests, `cli` secretwarn tests |
| 55 | The lifetime check covers `secrets:` only | CODE-ONLY | `applyConfig`. A scope choice rather than a safety rule; no harm if it widened |
| 56 | Probe log gaps | OBSOLETE | no probe history |
| 57 | Repositories by id, never by path | TESTED | `TestReposAreAddedByPathAndUsedById` |
| 58 | Repo add checked at the root through hostpath | TESTED | `TestRepoAtHomeIsRefused`; ids recomputed (`repos.go`) is CODE-ONLY |
| 59 | Files browser containment | OBSOLETE (host) / TESTED (guest) | `TestFiles` traversal, `testPathTraversal` |
| 60 | A Studio write has no fixture | OBSOLETE | no fixtures; the live API |
| 61 | Origin, loopback Host, token, content type | TESTED | the guard tests in `server` and `studio` |
| 62 | TCP needs a token; a network address needs TLS | TESTED | `TestOpenListenerRefusesAnUnguardedPort` |
| 63 | WebSocket subset: masking, close ends the loop | TESTED | `studio/websocket_test.go` |
| 64 | Typing at a live agent needs a token | TESTED | no tokenless TCP listener exists (row 62); the unix socket is owner-only |
| 65 | Facts as container labels | OBSOLETE | local records; labels decide nothing (`testLabels`) |
| 66 | A reference is matched against our own listing | TESTED | `testUnknownSandbox`, `testNameConflict`, `TestNewIDIsNotAName`, `TestOnlyItsOwnContainersAreRemoved` |
| 67 | A live sandbox wins over a dead one of the same name | TESTED | `TestANameResolvesToTheLiveSandbox` |
| 68 | `kill` never infers its target | TESTED | `TestKillNeedsANamedTarget` |
| 69 | Ctrl-C while watching does not stop the agent | TESTED | `TestInterruptDetachesAWatcherAndStopsAForegroundRun` |
| 70 | Repository text printed through termsafe | TESTED | `TestCleanRemovesEverythingATerminalInterprets`, `TestPrintEventsIsTerminalSafe` |
| 71 | No profile relaxes the boundary | TESTED | refusals under both profiles; no degradation path remains to warn about |
| 72 | The profile is the base layer; a project may raise, never lower | TESTED | `TestValidateProfileCatchesEachWeakening`, `TestResolveProfileLetsAProjectRaiseButNeverLower` |
| 73 | A flag outranks the files, not the profile | TESTED | `TestNetworkOverrideCannotEscapeTheProfile`, `TestNetworkFollowsTheConfigAndTheProfile` |
| 74 | prod keeps no persisted login | TESTED | `TestValidateProfileCatchesEachWeakening`, `TestProdTurnsPersistedLoginsOff` |
| 75 | Dev's default egress is the baseline allowlist | TESTED | `TestDefaultPolicyIsValid`, `testNetworkDefault` |
| 76 | `--user root` makes the allowlist yield | OBSOLETE | no `--user` |
| 77 | doctor's host preflight | OBSOLETE | the server refuses at create |
| 78 | A project config cannot set privileged keys; every field classified | TESTED | `TestProjectConfigRefusesPrivilegedKeys`, `TestEveryConfigFieldIsClassified` |
| 79 | `--config` and the user's config are trusted | TESTED | `TestExplicitConfigIsTrusted`, `TestUserConfigIsTrusted` |
| 80 | Discovery stops at the repo root | TESTED | `TestFindProjectConfig_BoundedWalk` |
| 81 | Root-phase scripts and PATH | OBSOLETE | no root phase in the guest |
| 82 | `.git` pointer checks | OBSOLETE | no `.git` mount |
| 83 | Reserved env refused from flags and the API | TESTED | `TestResolveRefusesReservedEnvAndUnlistedImages`, `testReservedEnv`, `TestBuildEnv` |
| 84 | Reserved env refused from config `env:`/`secrets:` | TESTED | `TestLoadConfigValidatesTheMergedConfig`. The list still names beta.15 variables nothing reads. Keeping them only tightens |
| 85 | `run` takes `--`; wrappers split their flags | TESTED | `TestSplitWrapperArgs` |
| 86 | `wrapperSubcommands` | OBSOLETE | agents live under `agent` |
| 87 | Detached: exit code and logs are the record | TESTED | `TestFollowAfterExit`, `testLateFollower`, `TestDetachRefusesCheckpointEvery` |
| 88 | One agent per branch | TESTED | `testNameConflict`; fleet `sandboxName` |
| 89 | Console argv; prompt seeded only where described | TESTED | `TestConsoleSeedsOnlyWhereTheDescriptorSaysHow` |
| 90 | No console with verify; a fleet never uses one | CODE-ONLY | `runner.go` always uses `Autonomous`, and no API exposes another path |
| 91 | Console transcript from the sandbox only | TESTED | `TestReadTranscriptFromTheSandbox` |
| 92 | Keystroke order; resize nudge | OBSOLETE / CODE-ONLY | one ordered WebSocket; the nudge is in `terminal.tsx` |
| 93 | Logins persisted apart from the host's own; opt-out | TESTED | `TestWritePrivateRefusesSymlinks`, `TestSavedLoginsCarryNoCommands` |
| 94 | One wrapper contract; lazy, pinned installs | TESTED | `TestInteractiveAgentsAreComplete`, `TestToolsCoverEveryBootstrappedAgent`, pin tests |
| 95 | Headless adds the skip flag; the wrapper adds nothing | TESTED / CODE-ONLY | `TestAutonomousStillCarriesTheSkipFlag`, `TestConsoleAsksUnlessToldNotTo`; "adds nothing" is `routed.go` |
| 96 | Status line, history mounts | OBSOLETE | not carried over |
| 97 | Non-root by default; stdlib + cobra + yaml.v3 | TESTED | conformance `ProcessesDoNotRunAsRoot` (passes on Firecracker); `go.mod` tidied to cobra (with pflag) and yaml.v3 |

Still CODE-ONLY, and acceptable as stated: rows 21, 55, 58 (the id half), 90,
92 and 95 (the wrapper half). Still to run on a real host: rows 9 and 97 on
the Mac (end-to-end rows 15 and 31), and peer isolation with a network on
Linux (row 31).
