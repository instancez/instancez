package telemetry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/posthog/posthog-go"
	"gopkg.in/yaml.v3"
)

// Inputs below are the exact shapes the leak audit sent out.

var corpusYAML = expand(`version: 1
# password: hunter2-in-comment
# DATABASE_URL=postgres://u:commentpw@h/db
project:
  name: &projname "acme-alias-hunter2"  # api_key = sk_comment_hunter2
providers:
  storage:
    type: s3
    bucket: acme-prod-bucket
    region: us-east-1
    access_key_id: @@AWSID@@
    secret_access_key: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
  email:
    type: resend
    api_key: re_AbCdEf_hunter2resend
    default_from_email: alice.smith@acme-corp.com
auth:
  jwt_expiry: 15m
  email:
    verify_email: false
tables:
  todos:
    rls_enabled: true
    fields:
      - name: id
        type: bigserial
        primary_key: true
      - name: password
        type: text
        default: "hunter2-default"
        check: "password <> 'hunter2-check'"
    rls:
      - operations: [select]
        using: "current_setting('app.pw') = 'hunter2-rls'"
functions:
  hello:
    file: functions/hello.js
    runtime: node
    env: &baseenv
      PW: pw-hunter2
      PASS_WORD: password-hunter2
      STRIPE: stripe-hunter2
      token_value: tokval-hunter2
      HMAC: hmac-hunter2-value
      SIGNING_SALT: salt-hunter2
      WEBHOOK_SIGNING: whsig-hunter2
      DATABASE_URI: "host=db user=bob password=dbpw-hunter2"
      AUTH_HEADER: "Basic YWxpY2U6aHVudGVyMg=="
      CERT: |
        @@PEMBEGIN@@
        MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKj
        MzEfYyjiWA4R4/M2bS1GB4t7NXp98C3SC6dVMvDuictGeurT8jNbvJZHtCSuYEvu
        -----END PRIVATE KEY-----
      ALIASED: *projname
  hello2:
    file: functions/hello.js
    runtime: node
    env:
      <<: *baseenv
      EXTRA_SECRET: extra-hunter2
      sk_live_KeyNameSecret1234: keyed
  hello3:
    file: functions/link.js
    runtime: node
    env: {password: flow-hunter2, pw: flowpw-hunter2}
`)

var corpusJS = expand("// owner: alice.smith@acme-corp.com, laptop /Users/alice/acme\n" +
	`// api_key = sk_comment_hunter2 and passwd=hunter2-commentpw
import { createClient } from "@supabase/supabase-js";
const lib = require("node:child_process");
const STRIPE = "@@STRIPE@@";
const RK = "@@RK@@";
const WH = "@@WH@@";
const AWS_ID = "@@AWSID@@";
const AWS_SECRET = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY";
const AWS_SECRET2 = "abc/def+ghi/jkl+mno/pqr+stu/vwx+yz0/123+";
const JWT = "@@JWT@@";
const DB = "postgres://admin:Tr0ub4dor&3@db.acme-corp.internal:5432/prod";
const GH = "@@GH@@";
const GHO = "@@GHO@@";
const GHPAT = "@@GHPAT@@";
const SLACK = "@@SLACK@@";
const SLACK_HOOK = "@@SLACKHOOK@@";
const OPENAI = "@@OPENAI@@";
const ANTH = "@@ANTH@@";
const GOOGLE = "@@GOOGLE@@";
const SHORTPW = "hunter2";
const password = 'CorrectHorseBattery';
const tpl = ` + "`CorrectHorseTemplate`" + `;
const headers = { Authorization: "Bearer abc.def-ghi_jkl", Other: "Basic YWxpY2U6aHVudGVyMg==" };
const url = "https://api.example.com/x?client_secret=cs-hunter2&X-Amz-Signature=amzsig-hunter2";
const kv = "password: hunter2-colon and \"password\":\"hunter2-json\" and password = hunter2-spaced and token=tok-hunter2";
const pem = ` + "`" + `@@RSABEGIN@@
MIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun
VTLw7onLRnrq0/IzW7yWR7QkrmBL7jTKEn5u+qKhbwKfBstIs+bMY2Zkp18gnTxK
LxoS2tFczGkPLPgizskuemMghRniWaoLcyehkd3qqGElvW/VDL5AaWTg0nLVkjRo
-----END RSA PRIVATE KEY-----` + "`" + `;
export default async (req) => new Response("hi");
const t = flag ? "tern-hunter2-value" : "case-hunter2-other";
switch (x) { case "case-label-hunter2x": break; }
// seCRETzw` + "\u200b" + `Q9vX2kL0pQ7rT7
/* block secret Kx7mP2qL9wZ4vB8n
   second line */
const b64 = "dGhpcyBpcyBhIHNlY3JldA== tail";
const host = "connect to db7.acme-corp.internal now";
const creds = "curl -u deploy:hunter2script https://example.com and mysql -phunter2pass -h x";
const re = /` + "`" + `/; const after = "spaced secret Zq9vX2kL0pQ7rT";
`)

const corpusPkg = `{"name": "fn", "scripts": {"deploy": "curl -u deploy:hunter2script https://db.acme-corp.internal/x", "db": "mysql -phunter2pass -h dbhost"}}`

// corpusErrors are error texts the CLI produced or could produce.
var corpusErrors = []string{
	`database: provision roles: parse DSN: parse "postgres://saedx1:hunt er2@127.0.0.1:55432/acme_customer_db": net/url: invalid userinfo`,
	`panic: dial postgres://alice:hunt er2@db.acme-corp.internal/x failed`,
	"database: provision roles: connect: failed to connect to `user=saedx1 database=acme_customer_db`:\n\t127.0.0.1:55432 (127.0.0.1): tls error: server refused TLS connection\n\t127.0.0.1:55432 (db.acme-corp.internal): failed SASL auth: FATAL: password authentication failed for user \"saedx1\" (SQLSTATE 28P01)",
	"connect: failed to connect to `user=x database=y`: 127.0.0.1:55432 (127.0.0.1): server error: FATAL: database \"nonexistent_db_acme\" does not exist (SQLSTATE 3D000)",
	"connect: failed to connect to `user=x database=y`:\n\thostname resolving error: lookup db.acme-corp.internal: no such host\n\tlookup db.acme-corp.internal: no such host",
	"connect: failed to connect to `user=x database=y`:\n\thostname resolving error: lookup ss: no such host",
	"dial error: dial tcp db.acme-corp.internal:5432: connect: connection refused",
	`ERROR: invalid input syntax for type integer: "hunter2-int-default" (SQLSTATE 22P02)`,
	`ERROR: invalid input value for enum mood: "hunter2-enum" (SQLSTATE 22P02)`,
	`ERROR: role 'alice.smith' does not exist`,
	"open /srv/acme-corp/alice.smith@acme-corp.com/instancez.yaml: permission denied",
	`panic at /Users/bob/acme/x.go:12 and C:\Users\carol\acme\y.go`,
	`invalid argument "hunter2-port" for "--port" flag: strconv.ParseInt: parsing "hunter2-port": invalid syntax`,
	`unknown command "Hunter2Cmd" for "inz"`,
	"auth failed: Authorization: Bearer abc.def-ghi_jkl and Basic YWxpY2U6aHVudGVyMg==",
	"bad key wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY and " + tokSLACK,
	"GET https://api.example.com/x?client_secret=cs-hunter2&X-Amz-Signature=amzsig-hunter2 failed",
	"config: password: hunter2-colon, \"password\":\"hunter2-json\", password = hunter2-spaced, passwd=hunter2-pwd, secret=hunter2-sec, token=hunter2-tok",
	"mail to alice@localhost failed",
	"bad key inz_secret_NhYNYorO6XWciNmyID7w2FZYxS-_91iY",
	"couldn't connect: role 'alice.smith' denied",
	`parse "postgres://alice.smith:p@ss/w ord\"q@h:5432/acme_db": invalid userinfo`,
	"tls error: x509: certificate is valid for db.acme-corp.internal, *.acme-corp.internal, not db2.acme-corp.internal",
	"x509: certificate is not valid for any names, but wanted to match db.acme-corp.internal",
	"x509: certificate is valid for 120 names, but none matched db.acme-corp.internal",
	"x509: cannot validate certificate for db.acme-corp.internal because it doesn't contain any IP SANs",
	"config: pass=hunter2-pass, DB_PASS: hunter2-dbpass",
	"login with " + tokPAT + " failed",
}

// corpusSecrets must not survive any outbound path, not even as 8-char fragments.
var corpusSecrets = []string{
	"hunter2", "hunt er2", "saedx1", "alice", "acme_db", "acme-corp", "acme_customer_db", "nonexistent_db_acme", "lookup ss",
	"commentpw", "acme-prod-bucket", tokAWSID, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	"abc/def+ghi/jkl+mno/pqr+stu/vwx+yz0/123+", "YWxpY2U6aHVudGVyMg==", "abc.def-ghi_jkl",
	"MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKj", "MzEfYyjiWA4R4/M2bS1GB4t7NXp98C3SC6dVMvDuictGeurT8jNbvJZHtCSuYEvu",
	"MIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun", "VTLw7onLRnrq0/IzW7yWR7QkrmBL7jTKEn5u+qKhbwKfBstIs+bMY2Zkp18gnTxK",
	"LxoS2tFczGkPLPgizskuemMghRniWaoLcyehkd3qqGElvW/VDL5AaWTg0nLVkjRo",
	tokSTRIPE, tokRK, tokWH,
	tokJWTHDR, "Tr0ub4dor", tokGH, tokGHO,
	tokGHPAT,
	tokSLACK, tokSLACKTAIL,
	tokOPENAI, tokANTH,
	tokGOOGLE, "CorrectHorse", "seCRETzw", "Q9vX2kL0pQ7rT7", "Kx7mP2qL9wZ4vB8n",
	"dGhpcyBpcyBhIHNlY3JldA==", "Zq9vX2kL0pQ7rT", "AbC123xyz789QWEasd", "sk_live_KeyNameSecret1234", "NhYNYorO6XWciNmyID7w2FZYxS-_91iY", "bob", "carol",
}

func leaked(out string) []string {
	var found []string
	for _, s := range corpusSecrets {
		if len(s) < 10 {
			if strings.Contains(out, s) {
				found = append(found, s)
			}
			continue
		}
		for i := 0; i+8 <= len(s); i++ {
			if strings.Contains(out, s[i:i+8]) {
				found = append(found, s+" ("+s[i:i+8]+")")
				break
			}
		}
	}
	return found
}

func TestLeakCorpus(t *testing.T) {
	home := "/home/saedx1"
	for i, msg := range corpusErrors {
		for name, out := range map[string]string{"Sanitize": Sanitize(msg, home), "SanitizeStack": SanitizeStack(msg, home)} {
			if l := leaked(out); len(l) > 0 {
				t.Errorf("error %d via %s leaked %v:\n%s", i, name, l, out)
			}
		}
	}

	dir := t.TempDir()
	cfg := filepath.Join(dir, "instancez.yaml")
	_ = os.MkdirAll(filepath.Join(dir, "functions"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "functions", "hello.js"), []byte(corpusJS), 0o600)
	if err := os.WriteFile(cfg, []byte(corpusYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	class := func(error) (string, bool, string) { return "go:x", false, corpusErrors[0] }
	_ = os.WriteFile(filepath.Join(dir, "functions", "package.json"), []byte(corpusPkg), 0o600)
	r := BuildCrashReport("dev", errors.New("x"), []posthog.StackFrame{{Filename: "/home/saedx1/x.go", Function: "main.run"}}, cfg, map[string]string{"functions/hello.js": corpusJS, "functions/package.json": corpusPkg}, class, home)
	for name, out := range map[string]string{"config_yaml": r.ConfigYAML, "function": r.Functions["functions/hello.js"], "package.json": r.Functions["functions/package.json"], "message": r.ErrMessage, "stack": r.Frames[0].Filename + r.Frames[0].Function} {
		if out == "" {
			t.Errorf("%s empty", name)
		}
		if l := leaked(out); len(l) > 0 {
			t.Errorf("%s leaked %v:\n%s", name, l, out)
		}
	}
	var v any
	if err := yaml.Unmarshal([]byte(r.ConfigYAML), &v); err != nil {
		t.Errorf("config_yaml is not valid YAML: %v\n%s", err, r.ConfigYAML)
	}
}
