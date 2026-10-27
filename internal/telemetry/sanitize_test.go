package telemetry

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitize(t *testing.T) {
	jwt := fake("eyJ", "hbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.", "eyJzdWIiOiIxMjM0NTY3ODkwIn0.", "dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U")
	cases := []struct{ name, in, home, want string }{
		{"dsn", "dial postgres://u:p@h:5432/db failed", "", "dial postgres://<redacted> failed"},
		{"dsn no pass", "postgres://u@h/db", "", "postgres://<redacted>"},
		{"home", "open /home/x/proj/a.yaml", "/home/x", "open <path>/a.yaml"},
		{"home trailing slash", "open /home/x/a", "/home/x/", "open <path>/a"},
		{"home prefix of other user", "/home/xavier/a", "/home/x", "<path>/a"},
		{"home root ignored", "/etc/a", "/", "<path>/a"},
		{"dsn password with at and slash", "postgres://u:p@ss/w@h:5432/db", "", "postgres://<redacted>"},
		{"real home path", "open /home/u/repos/instancez/main/instancez.yaml", "/home/u", "open <path>/instancez.yaml"},
		{"long relative path kept", "functions/very/long/nested/dir/structure/handler.js", "", "functions/very/long/nested/dir/structure/handler.js"},
		{"base64 token", "key " + "dGhpc2lzYXZlcnlsb25nYmFzZTY0dG9rZW4xMjM0", "", "key <redacted>"},
		{"email", "user a@b.io bad", "", "user <email> bad"},
		{"jwt", "token " + jwt, "", "token <redacted>"},
		{"stripe", "key sk_live_abc123DEF456ghi", "", "key <redacted>"},
		{"posthog", "key phc_pTBwgBY5a4f2TsTMBhsqonSmTAC8duxSjFscRUfpKdPK", "", "key <redacted>"},
		{"aws", "id " + tokAWSID, "", "id <redacted>"},
		{"hex", "sha " + strings.Repeat("ab12", 10), "", "sha <redacted>"},
		{"short hex kept", "sha abc123", "", "sha abc123"},
		{"empty", "", "/home/x", ""},
		{"plain", "config invalid: tables.x", "/home/x", "config invalid: tables.x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sanitize(tc.in, tc.home); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeTruncates(t *testing.T) {
	got := Sanitize(strings.Repeat("a ", 500), "")
	if n := utf8.RuneCountInString(got); n != 300 {
		t.Fatalf("runes = %d", n)
	}
	got = Sanitize(strings.Repeat("é世 ", 400), "")
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != 300 {
		t.Fatalf("bad truncation: valid=%v runes=%d", utf8.ValidString(got), utf8.RuneCountInString(got))
	}
}

func TestScrubSecretsNoTruncation(t *testing.T) {
	in := strings.Repeat("x ", 500)
	if got := ScrubSecrets(in); got != in {
		t.Fatal("scrub changed or truncated plain text")
	}
	if got := ScrubSecrets("postgres://u:p@h/db " + tokAWSID); got != "postgres://<redacted> <redacted>" {
		t.Fatalf("got %q", got)
	}
}

func TestScrubPgxConnectMessage(t *testing.T) {
	msg := "failed to connect to `user=bob database=prod_db`: 127.0.0.1:5432 (localhost): dial error"
	got := Sanitize(msg, "")
	for _, leak := range []string{"bob", "prod_db"} {
		if strings.Contains(got, leak) {
			t.Errorf("leaked %q: %s", leak, got)
		}
	}
	if got != "failed to connect to <value>: <host>: dial error" {
		t.Errorf("shape lost: %s", got)
	}
}

func TestScrubConnKeywordForms(t *testing.T) {
	for in, leak := range map[string]string{
		"host=db.internal user=alice dbname=shop": "alice",
		"host=db.internal user='a b' port=1":      "db.internal",
		`database="my db"`:                        "my db",
		"db_pwd=hunter2":                          "hunter2",
		"my_api_key=zzz9":                         "zzz9",
		"DB_PASSWORD=s3cret":                      "s3cret",
	} {
		if got := ScrubSecrets(in); strings.Contains(got, leak) {
			t.Errorf("%q -> %q", in, got)
		}
	}
	if got := ScrubSecrets("port=5432 sslmode=disable"); got != "port=5432 sslmode=disable" {
		t.Errorf("over-scrubbed: %q", got)
	}
}

func TestStripHomeRepeatedAndPerHome(t *testing.T) {
	for i := 0; i < 3; i++ {
		if got := stripHome("open /home/a/x and /home/ab/y", "/home/a"); got != "open ~/x and /home/ab/y" {
			t.Fatalf("iteration %d: %q", i, got)
		}
	}
	if got := stripHome("open /home/b/x", "/home/b/"); got != "open ~/x" {
		t.Fatalf("got %q", got)
	}
}
