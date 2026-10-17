package cli

import (
	"strings"
	"testing"
	"time"
)

func TestExportFlags(t *testing.T) {
	t.Setenv("OTEL_AUTH", "Bearer s3cret")
	me, err := (&exportFlags{endpoint: "https://otlp.example.com", every: 30 * time.Second,
		headers: []string{"Authorization=OTEL_AUTH"}, headerSecrets: []string{"X-Key=otel-key"}}).export()
	if err != nil {
		t.Fatal(err)
	}
	if me.IntervalSecs != 30 || me.Headers["Authorization"] != "Bearer s3cret" || me.HeaderSecrets["X-Key"] != "otel-key" {
		t.Fatalf("%+v", me)
	}
	if me, err := (&exportFlags{}).export(); me != nil || err != nil {
		t.Fatalf("no flags: %+v, %v", me, err)
	}
	if me, err := (&exportFlags{endpoint: "none"}).export(); err != nil || me == nil || me.Endpoint != "" {
		t.Fatalf("none: %+v, %v", me, err)
	}
	for name, ef := range map[string]exportFlags{
		"unset env":        {endpoint: "https://x", headers: []string{"Authorization=NOT_SET_ANYWHERE"}},
		"no equals":        {endpoint: "https://x", headers: []string{"Authorization"}},
		"header alone":     {headers: []string{"Authorization=OTEL_AUTH"}},
		"none and headers": {endpoint: "none", headers: []string{"Authorization=OTEL_AUTH"}},
		"fraction":         {endpoint: "https://x", every: 1500 * time.Millisecond},
		"bad secret":       {endpoint: "https://x", headerSecrets: []string{"X-Key"}},
	} {
		_, err := ef.export()
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: the error holds the value: %v", name, err)
		}
	}
}
