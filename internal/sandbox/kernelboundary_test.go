package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// prod's demand for a kernel of its own, enforced where the run is.
//
// The reason it is here rather than in ValidateProfile is the first test below:
// `--runtime` reaches a run through Options and wins over the config, so a check
// against the resolved Config would pass while the container launched on runc.
// That is the persist_auth class of leak — "ValidateProfile validates the
// resolved Config, and the leak is in the Options" — and it is worth a test that
// names it.

// boundaryStub answers the two questions the enforcement asks, and records
// whether a container was ever started.
type boundaryStub struct {
	startStub
	support runtime.RuntimeSupport
}

func (b *boundaryStub) StrongerRuntimeSupport(context.Context) runtime.RuntimeSupport {
	return b.support
}

func prodSession(t *testing.T, rt runtime.Runtime) *Session {
	t.Helper()
	cfg := config.Default()
	cfg.Profile = config.ProfileProd
	return &Session{Cfg: cfg, Runtime: rt, Audit: audit.NopSink{}}
}

func TestProdRefusesAWeakRuntimeArrivingByFlag(t *testing.T) {
	// The host has kata registered, and the *config* names it — so any check made
	// against the resolved Config passes.
	rt := &boundaryStub{support: dockerHost("runc", "runc", "kata-runtime")}
	s := prodSession(t, rt)
	s.Cfg.Runtime = "kata-runtime"

	// And the flag puts the run back on a shared kernel.
	_, err := s.Start(context.Background(), Options{
		Project: t.TempDir(),
		Runtime: "runc",
		Command: []string{"true"},
	}, false)
	if err == nil {
		t.Fatal("--runtime runc defeated prod's demand for a kernel of its own")
	}
	if !strings.Contains(err.Error(), "neither the run nor the engine's default") {
		t.Errorf("the refusal does not explain what was wrong: %v", err)
	}
	if rt.started {
		t.Error("a container was started before the boundary was checked")
	}
}

func TestProdOnTheResolvedRuntime(t *testing.T) {
	kataHost := dockerHost("runc", "runc", "kata-runtime")

	t.Run("the flag can also satisfy it", func(t *testing.T) {
		// The mirror of the test above, and the reason the check reads the
		// resolved value rather than refusing flags outright: a run that names a
		// stronger runtime on the command line has asked for exactly what prod
		// wants.
		rt := &boundaryStub{support: kataHost}
		s := prodSession(t, rt)
		if _, err := s.Start(context.Background(), Options{
			Project: t.TempDir(),
			Runtime: "kata-runtime",
			Command: []string{"true"},
		}, false); err != nil {
			t.Fatalf("prod refused a run that selected kata on the command line: %v", err)
		}
		if !rt.started {
			t.Error("the run was accepted but no container was started")
		}
	})

	t.Run("a name no list recognises still satisfies it", func(t *testing.T) {
		// Registered with this engine, just not known to us — which is the case
		// gVisor's own installer produces.
		kataHost := dockerHost("runc", "runc", "kata-runtime", "runsc-hostnet")
		// gVisor's own installer produces runsc-hostnet, and an admin may
		// register a runtime under any name at all. The engine settles whether it
		// exists by refusing the launch; refusing here on a short allowlist would
		// be a refusal the operator cannot clear.
		rt := &boundaryStub{support: kataHost}
		s := prodSession(t, rt)
		if _, err := s.Start(context.Background(), Options{
			Project: t.TempDir(),
			Runtime: "runsc-hostnet",
			Command: []string{"true"},
		}, false); err != nil {
			t.Fatalf("prod refused a deliberately selected non-default runtime: %v", err)
		}
		if !rt.started {
			t.Error("the run was accepted but no container was started")
		}
	})
}

func TestProdAndTheDaemonsAnswer(t *testing.T) {
	cases := []struct {
		name    string
		support runtime.RuntimeSupport
		refuses bool
		says    string
	}{
		{
			// Nothing stronger reported is not proof that nothing exists: podman
			// answers with its active runtime only, and no signal distinguishes a
			// Linux host that could install Kata from a VM image its user does
			// not compose. Warned, not refused.
			name:    "nothing stronger reported",
			support: dockerHost("runc", "runc"),
		},
		{
			// The host that had already done the work: its default is strong, so
			// there is nothing to ask for.
			name:    "the engine's default is strong",
			support: dockerHost("runsc", "runc", "runsc"),
		},
		{
			// prod does not get to assume the answer it would prefer — and
			// `doctor` reaches the same verdict, which it did not before.
			name:    "the engine could not be asked",
			support: runtime.RuntimeSupport{},
			refuses: true,
			says:    "could not be asked",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &boundaryStub{support: tc.support}
			s := prodSession(t, rt)
			_, err := s.Start(context.Background(), Options{Project: t.TempDir(), Command: []string{"true"}}, false)
			if tc.refuses {
				if err == nil {
					t.Fatalf("prod accepted a host that answered %+v", tc.support)
				}
				if !strings.Contains(err.Error(), tc.says) {
					t.Errorf("the refusal does not say why: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("prod refused a host it should accept (%+v): %v", tc.support, err)
			}
		})
	}
}

// dev is untouched by all of it: the same hosts, the same runs, no refusals.
func TestDevNeverDemandsAKernelOfItsOwn(t *testing.T) {
	for _, support := range []runtime.RuntimeSupport{
		dockerHost("runc", "runc"),
		dockerHost("runc", "runc", "kata-runtime"),
		{},
	} {
		rt := &boundaryStub{support: support}
		s := prodSession(t, rt)
		s.Cfg.Profile = config.ProfileDev
		if _, err := s.Start(context.Background(), Options{Project: t.TempDir(), Command: []string{"true"}}, false); err != nil {
			t.Errorf("dev refused a run over the runtime (%+v): %v", support, err)
		}
	}
}

// TestProdArgvCarriesTheRuntimeItDemands covers what a real prod run on a host
// with a stronger runtime actually sends to the engine.
//
// The end-to-end launch test cannot: it needs a daemon that has Kata or gVisor
// registered, which no CI machine here has. What can be checked without one is
// that the runtime prod demands is rendered *alongside* the rest of prod's argv
// rather than instead of any of it — the confinement flags and the boundary flag
// are not alternatives.
func TestProdArgvCarriesTheRuntimeItDemands(t *testing.T) {
	cfg := config.Default()
	cfg.Profile = config.ProfileProd
	cfg.Security.Seccomp = config.SeccompRequired
	cfg.Runtime = "kata-runtime"

	spec, err := BuildSpec(cfg, Options{Project: t.TempDir(), Command: []string{"true"}})
	if err != nil {
		t.Fatalf("BuildSpec: %v", err)
	}
	if spec.Runtime != "kata-runtime" {
		t.Fatalf("spec.Runtime = %q, want the configured runtime", spec.Runtime)
	}

	argv := strings.Join(runtime.BuildArgs(spec), " ")
	for _, want := range []string{
		"--runtime kata-runtime",
		"--cap-drop ALL",
		"--security-opt no-new-privileges",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("prod argv does not contain %q:\n%s", want, argv)
		}
	}
}

// dockerHost is a complete registered set with a named default, the shape docker
// reports.
func dockerHost(def string, all ...string) runtime.RuntimeSupport {
	var strong []string
	for _, n := range all {
		if runtime.StrongerRuntime(n) {
			strong = append(strong, n)
		}
	}
	return runtime.RuntimeSupport{All: all, Registered: strong, Default: def, Complete: true, Known: true}
}

// TestProdPermitsAnUnrecognisedRuntimeWithoutVouchingForIt is the pair of claims
// that has to hold together: an operator who names something this tool does not
// know is not refused — gVisor's own installer produces runsc-hostnet — and the
// tool does not thereby assert they got a kernel of their own, because
// sysbox-runc is also a non-default name and shares the host kernel.
func TestProdPermitsAnUnrecognisedRuntimeWithoutVouchingForIt(t *testing.T) {
	rt := &boundaryStub{support: dockerHost("runc", "runc", "sysbox-runc")}
	s := prodSession(t, rt)
	if _, err := s.Start(context.Background(), Options{
		Project: t.TempDir(),
		Runtime: "sysbox-runc",
		Command: []string{"true"},
	}, false); err != nil {
		t.Fatalf("prod refused a deliberately selected runtime: %v", err)
	}
	if !rt.started {
		t.Error("the run was accepted but no container was started")
	}
	// And the tool's own answer about that container says it is not stronger.
	if (runtime.ContainerInfo{Runtime: "sysbox-runc"}).StrongerIsolation() {
		t.Error("sysbox-runc is reported as a kernel of its own")
	}
}

// A runtime this engine has not registered fails before the launch that would
// have failed anyway — but only where the engine's list is complete.
func TestProdOnARuntimeTheEngineDoesNotHave(t *testing.T) {
	t.Run("docker with a complete list", func(t *testing.T) {
		rt := &boundaryStub{support: dockerHost("runc", "runc")}
		s := prodSession(t, rt)
		_, err := s.Start(context.Background(), Options{
			Project: t.TempDir(), Runtime: "kata-runtime", Command: []string{"true"},
		}, false)
		if err == nil {
			t.Fatal("prod accepted a runtime this engine has not registered")
		}
		if !strings.Contains(err.Error(), "kata-runtime") {
			t.Errorf("the refusal does not name it: %v", err)
		}
	})

	t.Run("podman which names only its active runtime", func(t *testing.T) {
		// The same shape must not refuse here: podman reports what it is using,
		// so an absent name is not evidence of absence.
		rt := &boundaryStub{support: runtime.RuntimeSupport{
			All: []string{"crun"}, Default: "crun", Known: true,
		}}
		s := prodSession(t, rt)
		if _, err := s.Start(context.Background(), Options{
			Project: t.TempDir(), Runtime: "kata-runtime", Command: []string{"true"},
		}, false); err != nil {
			t.Fatalf("prod refused kata on a podman host that may well have it: %v", err)
		}
	})
}
