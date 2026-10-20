package cli

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// doctor says what a GPU endpoint offers and how many are free; run --gpus
// asks for them, and a run the node has none free for says why.
func TestDoctorAndRunWithGPUs(t *testing.T) {
	be := fake.New(api.CapGPU)
	s := &server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), GPUs: []string{"0000:01:00.0", "0000:41:00.0"}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	useEndpoint(t, srv.URL)

	out, err := runCLI(t, "doctor")
	if err != nil || !strings.Contains(out, "gpus:      up to 2 per sandbox (--gpus); 2 of 2 free on this node") {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "run", "--keep", "--gpus", "2", "--", "true"); err != nil {
		t.Fatalf("run --gpus 2: %v\n%s", err, out)
	}
	out, _ = runCLI(t, "doctor")
	if !strings.Contains(out, "0 of 2 free") {
		t.Fatalf("doctor with both held:\n%s", out)
	}
	out, err = runCLI(t, "run", "--gpus", "1", "--", "true")
	if err == nil || !strings.Contains(out+err.Error(), "0 GPU(s) free of 2") {
		t.Fatalf("a run with none free: %v\n%s", err, out)
	}
}

// An endpoint without GPUs: doctor says nothing of them.
func TestDoctorWithoutGPUs(t *testing.T) {
	be := fake.New()
	srv := httptest.NewServer((&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities())}).Handler())
	defer srv.Close()
	useEndpoint(t, srv.URL)
	if out, err := runCLI(t, "doctor"); err != nil || strings.Contains(out, "gpus:") {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
}
