package studioapi

import "testing"

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"":           0,
		"--":         0,
		"0B":         0,
		"512B":       512,
		"1KiB":       1024,
		"1.5MiB":     1572864,
		"1GiB":       1 << 30,
		"1kB":        1000,
		"2MB":        2_000_000,
		"1TiB":       1 << 40,
		"not-a-size": 0,
	}
	for in, want := range cases {
		if got := parseSize(in); got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseMemUsage(t *testing.T) {
	used, limit := parseMemUsage("12.34MiB / 1.952GiB")
	mib, gib := 12.34, 1.952 // non-constant, so the int64 conversion below is a runtime truncation, not a constant one
	wantUsed := int64(mib * float64(1<<20))
	wantLimit := int64(gib * float64(1<<30))
	if used != wantUsed {
		t.Errorf("used = %d, want %d", used, wantUsed)
	}
	if limit != wantLimit {
		t.Errorf("limit = %d, want %d", limit, wantLimit)
	}

	used, limit = parseMemUsage("--")
	if used != 0 || limit != 0 {
		t.Errorf("parseMemUsage(\"--\") = (%d, %d), want (0, 0)", used, limit)
	}
}

func TestParsePercent(t *testing.T) {
	cases := map[string]float64{
		"0.50%":  0.5,
		"12.34%": 12.34,
		"--":     0,
		"":       0,
	}
	for in, want := range cases {
		if got := parsePercent(in); got != want {
			t.Errorf("parsePercent(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseIntOr(t *testing.T) {
	if got := parseIntOr("42", -1); got != 42 {
		t.Errorf("parseIntOr(42) = %d, want 42", got)
	}
	if got := parseIntOr("--", -1); got != -1 {
		t.Errorf("parseIntOr(--) = %d, want fallback -1", got)
	}
}
