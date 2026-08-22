package region

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseEnv(t *testing.T) {
	cases := map[string]struct {
		value string
		want  string
		ok    bool
	}{
		"empty":         {value: "", ok: false},
		"cn":            {value: "cn", want: CN, ok: true},
		"CN":            {value: "CN", want: CN, ok: true},
		"china":         {value: "china", want: CN, ok: true},
		"intl":          {value: "intl", want: INTL, ok: true},
		"international": {value: "INTERNATIONAL", want: INTL, ok: true},
		"unknown":       {value: "eu", want: INTL, ok: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := parseEnv(tc.value)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("parseEnv(%q) = %q, %v, want %q, %v", tc.value, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestDetectUsesEnvironmentOverride(t *testing.T) {
	t.Setenv(EnvName, "CN")
	if got := Detect(); got != CN {
		t.Fatalf("Detect() = %q, want %q", got, CN)
	}
	t.Setenv(EnvName, "intl")
	if got := Detect(); got != INTL {
		t.Fatalf("Detect() = %q, want %q", got, INTL)
	}
}

func TestDetectDefaultsToINTLInTestsWithoutEnv(t *testing.T) {
	t.Setenv(EnvName, "")
	if got := Detect(); got != INTL {
		t.Fatalf("Detect() = %q, want %q in tests", got, INTL)
	}
}

func TestLookupIPRegion(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		body string
		hang bool
		want string
	}{
		"china": {body: "CN\n", want: CN},
		"us":    {body: "US", want: INTL},
		"empty": {body: "   ", want: INTL},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			if got := lookupIPRegion(server.URL); got != tc.want {
				t.Fatalf("lookupIPRegion() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLookupIPRegionDefaultsToCNOnError(t *testing.T) {
	if got := lookupIPRegion("http://127.0.0.1:1"); got != CN {
		t.Fatalf("lookupIPRegion() = %q, want %q", got, CN)
	}
}
