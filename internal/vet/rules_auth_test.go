package vet

import (
	"slices"
	"testing"
)

func TestSecretRules(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"default config", "tables: {}\n", nil},
		{"email literal", "providers:\n  email:\n    type: resend\n    api_key: re_live_123\n", []string{"hardcoded-secret"}},
		{"email env ref", "providers:\n  email:\n    type: resend\n    api_key: ${INSTANCEZ_ENV_KEY}\n", nil},
		{"env ref with default", "providers:\n  email:\n    type: resend\n    api_key: ${INSTANCEZ_ENV_KEY:-dev}\n", nil},
		{"empty secret", "providers:\n  email:\n    type: resend\n    api_key: \"\"\n", nil},
		{"storage both", "providers:\n  storage:\n    type: s3\n    access_key_id: AKIA1\n    secret_access_key: abc\n", []string{"hardcoded-secret", "hardcoded-secret"}},
		{"storage secret only literal", "providers:\n  storage:\n    type: s3\n    access_key_id: ${A}\n    secret_access_key: abc\n", []string{"hardcoded-secret"}},
		{"oauth two literals", "auth:\n  oauth:\n    google: {client_id: a, client_secret: s1}\n    github: {client_id: b, client_secret: s2}\n    gitlab: {client_id: c, client_secret: \"${X}\"}\n", []string{"hardcoded-secret", "hardcoded-secret"}},
		{"function env secret name", "functions:\n  f:\n    env:\n      API_TOKEN: abc\n      REGION: eu\n    auth_required: true\n", []string{"hardcoded-secret"}},
		{"function env ref", "functions:\n  f:\n    env:\n      API_TOKEN: ${INSTANCEZ_ENV_T}\n    auth_required: true\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vetIDs(t, tc.src); !slices.Equal(got, tc.want) {
				t.Errorf("rules = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSecretOrderAndPath(t *testing.T) {
	r, err := Run([]byte("auth:\n  oauth:\n    google: {client_id: a, client_secret: s1}\n    github: {client_id: b, client_secret: s2}\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range r.Findings {
		if f.Rule == "hardcoded-secret" {
			paths = append(paths, f.Path)
		}
	}
	want := []string{"auth.oauth.github.client_secret", "auth.oauth.google.client_secret"}
	if !slices.Equal(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func sevOf(t *testing.T, src, rule string) (Severity, bool) {
	t.Helper()
	r, err := Run([]byte(src), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Findings {
		if f.Rule == rule {
			return f.Severity, true
		}
	}
	return 0, false
}

func TestJWTExpiry(t *testing.T) {
	cases := []struct {
		exp  string
		want Severity
		hit  bool
	}{
		{"15m", 0, false},
		{"1h", 0, false},
		{"61m", Medium, true},
		{"24h", Medium, true},
		{"24h1s", High, true},
		{"720h", High, true},
		{"${INSTANCEZ_ENV_EXP:-1h}", 0, false},
		{"soon", 0, false},
		{"7d", 0, false},
	}
	for _, tc := range cases {
		got, hit := sevOf(t, "auth:\n  jwt_expiry: \""+tc.exp+"\"\n", "jwt-expiry-long")
		if hit != tc.hit || (hit && got != tc.want) {
			t.Errorf("%s: got %v/%v, want %v/%v", tc.exp, got, hit, tc.want, tc.hit)
		}
	}
}

func TestSignupUnverified(t *testing.T) {
	cases := []struct {
		name, src string
		hit       bool
	}{
		{"default", "tables: {}\n", true},
		{"signup true", "auth:\n  allow_signup: true\n", true},
		{"signup false", "auth:\n  allow_signup: false\n", false},
		{"verified", "auth:\n  email:\n    verify_email: true\n", false},
		{"verify false", "auth:\n  email:\n    verify_email: false\n", true},
		{"templates only", "auth:\n  email:\n    templates: {}\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := slices.Contains(vetIDsOpts(t, tc.src, Options{}), "signup-unverified-email")
			if got != tc.hit {
				t.Errorf("hit = %v, want %v", got, tc.hit)
			}
		})
	}
	if got := vetIDsOpts(t, "tables: {}\n", Options{}); !slices.Equal(got, []string{"signup-unverified-email"}) {
		t.Errorf("default config findings = %v", got)
	}
}

func TestAnonymousSignins(t *testing.T) {
	ign := quiet
	if got := vetIDsOpts(t, "auth:\n  allow_anonymous: true\n", ign); !slices.Equal(got, []string{"anonymous-signins"}) {
		t.Errorf("on = %v", got)
	}
	for _, src := range []string{"auth:\n  allow_anonymous: false\n", "tables: {}\n"} {
		if got := vetIDsOpts(t, src, ign); got != nil {
			t.Errorf("%q = %v", src, got)
		}
	}
}

func TestRedirects(t *testing.T) {
	list := func(u string) string { return "auth:\n  redirect_urls: [\"" + u + "\"]\n" }
	cases := []struct {
		name, src string
		want      Severity
		hit       bool
	}{
		{"https fine", list("https://app.example.com/cb"), 0, false},
		{"http remote", list("http://app.example.com"), Medium, true},
		{"http localhost", list("http://localhost:3000"), Low, true},
		{"http 127", list("http://127.0.0.1:3000/cb"), Low, true},
		{"https localhost fine", list("https://localhost:3000"), 0, false},
		{"wildcard https", list("https://*.example.com"), Low, true},
		{"wildcard http", list("http://*.example.com"), Low, true},
		{"malformed", list("http://[::1"), 0, false},
		{"empty entry", list(""), 0, false},
		{"relative", list("/cb"), 0, false},
		{"oauth http remote", "auth:\n  oauth:\n    google: {client_id: a, client_secret: \"${S}\", redirect_url: \"http://x.example.com/cb\"}\n", Medium, true},
		{"oauth https", "auth:\n  oauth:\n    google: {client_id: a, client_secret: \"${S}\", redirect_url: \"https://x.example.com/cb\"}\n", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, hit := sevOf(t, tc.src, "redirect-insecure")
			if hit != tc.hit || (hit && got != tc.want) {
				t.Errorf("got %v/%v, want %v/%v", got, hit, tc.want, tc.hit)
			}
		})
	}
}

func TestServerRules(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"default", "tables: {}\n", nil},
		{"no origins", "server:\n  cors:\n    origins: []\n", nil},
		{"wildcard", "server:\n  cors:\n    origins: [\"*\"]\n", []string{"cors-wildcard"}},
		{"null origin", "server:\n  cors:\n    origins: [\"null\"]\n", []string{"cors-null-origin"}},
		{"both plus real", "server:\n  cors:\n    origins: [\"https://a.com\", \"*\", \"null\"]\n", []string{"cors-null-origin", "cors-wildcard"}},
		{"specific", "server:\n  cors:\n    origins: [\"https://a.com\"]\n", nil},
		{"limit zero defaults", "server:\n  max_limit: 0\n", nil},
		{"limit 1000", "server:\n  max_limit: 1000\n", nil},
		{"limit disabled", "server:\n  max_limit: -1\n", []string{"max-limit-disabled"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vetIDs(t, tc.src); !slices.Equal(got, tc.want) {
				t.Errorf("rules = %v, want %v", got, tc.want)
			}
		})
	}
	if got, _ := sevOf(t, "server:\n  cors:\n    origins: [\"*\"]\n", "cors-wildcard"); got != Low {
		t.Errorf("wildcard sev = %v", got)
	}
	if got, _ := sevOf(t, "server:\n  max_limit: -1\n", "max-limit-disabled"); got != Medium {
		t.Errorf("limit sev = %v", got)
	}
}
