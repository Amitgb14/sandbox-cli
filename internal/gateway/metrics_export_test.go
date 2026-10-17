package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

const otelToken = "Bearer otel-0123456789-do-not-leak"

// lastWrite is the last create or update body the node was sent.
func (tn *testNode) lastWrite(t *testing.T) map[string]any {
	t.Helper()
	tn.mu.Lock()
	defer tn.mu.Unlock()
	if len(tn.writes) == 0 {
		t.Fatal("the node was sent no create or update")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(tn.writes[len(tn.writes)-1]), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// header_secrets fills a metrics export's header from the tenant's sealed
// secret, at create and at update; the value reaches the node and nothing
// else, and another tenant cannot name it.
func TestMetricsExportHeaderSecrets(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, withKey, n1)
	ctx := ctxT(t)
	ana := tg.client("ana", "aa", ScopeRead, ScopeCreate, ScopeDelete, ScopeSecretsWrite)
	if err := ana.SetSecret(ctx, "otel-key", otelToken); err != nil {
		t.Fatal(err)
	}
	me := &api.MetricsExport{Endpoint: "https://collector.example", Headers: map[string]string{"X-Scope-OrgID": "ana"},
		HeaderSecrets: map[string]string{"authorization": "otel-key"}}
	sb, err := ana.CreateSandbox(ctx, api.CreateSandboxRequest{MetricsExport: me})
	if err != nil {
		t.Fatal(err)
	}
	sent, _ := json.Marshal(n1.lastWrite(t)["metrics_export"])
	if !strings.Contains(string(sent), `"Authorization":"`+otelToken+`"`) || strings.Contains(string(sent), "header_secrets") {
		t.Fatalf("the node was sent %s", sent)
	}
	raw, _ := json.Marshal(sb)
	if sb.MetricsExport == nil || strings.Join(sb.MetricsExport.HeaderNames, ",") != "Authorization,X-Scope-Orgid" || strings.Contains(string(raw), otelToken) {
		t.Fatalf("the client got %s", raw)
	}

	// An update is resolved the same way.
	if _, err := ana.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{MetricsExport: &api.MetricsExport{
		Endpoint: "https://collector.example", HeaderSecrets: map[string]string{"Authorization": "otel-key"}}}); err != nil {
		t.Fatal(err)
	}
	if sent, _ := json.Marshal(n1.lastWrite(t)["metrics_export"]); !strings.Contains(string(sent), otelToken) {
		t.Fatalf("the update sent %s", sent)
	}

	for name, me := range map[string]*api.MetricsExport{
		"no such secret": {Endpoint: "https://collector.example", HeaderSecrets: map[string]string{"Authorization": "nope"}},
		"both":           {Endpoint: "https://collector.example", Headers: map[string]string{"Authorization": "x"}, HeaderSecrets: map[string]string{"authorization": "otel-key"}},
		"bad name":       {Endpoint: "https://collector.example", HeaderSecrets: map[string]string{"Authorization": "../x"}},
	} {
		_, err := ana.CreateSandbox(ctx, api.CreateSandboxRequest{MetricsExport: me})
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), otelToken) {
			t.Errorf("%s: the error holds the value: %v", name, err)
		}
	}

	// Another tenant's secret of that name is not this one's.
	bo := tg.client("bo", "bb", ScopeRead, ScopeCreate)
	_, err = bo.CreateSandbox(ctx, api.CreateSandboxRequest{MetricsExport: &api.MetricsExport{
		Endpoint: "https://collector.example", HeaderSecrets: map[string]string{"Authorization": "otel-key"}}})
	wantCode(t, err, api.CodeInvalidRequest)
}

// Without a secrets key there are no secrets to name: refused, not sent
// without the header.
func TestMetricsExportHeaderSecretsNeedAKey(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	ana := tg.client("ana", "aa", ScopeRead, ScopeCreate)
	_, err := ana.CreateSandbox(ctxT(t), api.CreateSandboxRequest{MetricsExport: &api.MetricsExport{
		Endpoint: "https://collector.example", HeaderSecrets: map[string]string{"Authorization": "otel-key"}}})
	wantCode(t, err, api.CodeUnsupported)
}
