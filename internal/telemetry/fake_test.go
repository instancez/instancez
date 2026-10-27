package telemetry

import "strings"

func fake(parts ...string) string { return strings.Join(parts, "") }

var (
	tokRSABEGIN  = fake("-----BEGIN ", "RSA ", "PRIVATE KEY-----")
	tokPEMBEGIN  = fake("-----BEGIN ", "PRIVATE KEY-----")
	tokSTRIPE    = fake("sk_", "live_", "51HxQ2aBcDeFgHiJkLmNoPq")
	tokRK        = fake("rk_", "live_", "51HxQ2aBcDeFgHiJkLmNoPqRsTuV")
	tokWH        = fake("whsec", "_", "51HxQ2aBcDeFgHiJkLmNoPq")
	tokAWSID     = fake("AKIA", "IOSFODNN7EXAMPLE")
	tokJWT       = fake("eyJ", "hbGciOiJIUzI1NiJ9.", "eyJzdWIiOiIxIn0.", "c2lnbmF0dXJlZm9vYmFy")
	tokJWTHDR    = fake("eyJ", "hbGciOiJIUzI1NiJ9")
	tokGH        = fake("gh", "p_", "aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789")
	tokGHO       = fake("gh", "o_", "16C7e42F292c6912E7")
	tokGHPAT     = fake("github_", "pat_", "11ABCDEFG0123456789_", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUV")
	tokSLACK     = fake("xo", "xb-", "123456789012-1234567890123-", "AbCdEfGhIjKlMnOpQrStUvWx")
	tokSLACKHOOK = fake("https://hooks.slack", ".com/services/", "T00000000/", "B00000000/", "XXXXXXXXXXXXXXXXXXXXXXXX")
	tokSLACKTAIL = fake("T00000000/", "B00000000/", "XXXXXXXXXXXXXXXXXXXXXXXX")
	tokOPENAI    = fake("sk-", "proj-", "Ab3dEf_Gh1jK-Lm2nOp_Qr4sTu-Vw5xYz_0123-4567")
	tokANTH      = fake("sk-", "ant-", "api03-AbCdEf-GhIjKl_MnOpQr-StUvWx_YzAbCd-EfGh")
	tokGOOGLE    = fake("AI", "za", "SyA-1234567890abcdefghijklmnopqrstu")
	tokPAT       = fake("instancez_", "pat_", "AbC123xyz789QWEasd")
)

func expand(s string) string {
	pairs := []string{}
	pairs = append(pairs, "@@RSABEGIN@@", tokRSABEGIN)
	pairs = append(pairs, "@@PEMBEGIN@@", tokPEMBEGIN)
	pairs = append(pairs, "@@STRIPE@@", tokSTRIPE)
	pairs = append(pairs, "@@RK@@", tokRK)
	pairs = append(pairs, "@@WH@@", tokWH)
	pairs = append(pairs, "@@AWSID@@", tokAWSID)
	pairs = append(pairs, "@@JWT@@", tokJWT)
	pairs = append(pairs, "@@JWTHDR@@", tokJWTHDR)
	pairs = append(pairs, "@@GH@@", tokGH)
	pairs = append(pairs, "@@GHO@@", tokGHO)
	pairs = append(pairs, "@@GHPAT@@", tokGHPAT)
	pairs = append(pairs, "@@SLACK@@", tokSLACK)
	pairs = append(pairs, "@@SLACKHOOK@@", tokSLACKHOOK)
	pairs = append(pairs, "@@SLACKTAIL@@", tokSLACKTAIL)
	pairs = append(pairs, "@@OPENAI@@", tokOPENAI)
	pairs = append(pairs, "@@ANTH@@", tokANTH)
	pairs = append(pairs, "@@GOOGLE@@", tokGOOGLE)
	pairs = append(pairs, "@@PAT@@", tokPAT)
	return strings.NewReplacer(pairs...).Replace(s)
}
