package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/instancez/instancez/internal/adapter/oidc"
	"github.com/instancez/instancez/internal/domain"
)

const (
	appleAuthURL     = "https://appleid.apple.com/auth/authorize"
	appleTokenURL    = "https://appleid.apple.com/auth/token"
	appleIssuer      = "https://appleid.apple.com"
	maxAppleUserJSON = 2048
)

type appleProvider struct{ tokenURL string }

func (appleProvider) Name() string { return "apple" }

// ClientIDs splits a comma-separated client id list, dropping blanks.
func ClientIDs(s string) []string {
	var ids []string
	for _, id := range strings.Split(s, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// EmailVerifiedClaim reads an OIDC email_verified claim, which some IdPs send as a string.
func EmailVerifiedClaim(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	s, _ := v.(string)
	return s == "true"
}

func firstClientID(s string) string {
	if ids := ClientIDs(s); len(ids) > 0 {
		return ids[0]
	}
	return ""
}

func (appleProvider) AuthorizeURL(cfg *domain.OAuthProvider, state string) string {
	q := url.Values{
		"client_id": {firstClientID(cfg.ClientID)}, "redirect_uri": {cfg.RedirectURL},
		"response_type": {"code"}, "response_mode": {"form_post"},
		"scope": {"name email"}, "state": {state},
	}
	return appleAuthURL + "?" + strings.ReplaceAll(q.Encode(), "+", "%20")
}

func (a appleProvider) ExchangeCode(cfg *domain.OAuthProvider, code string) (*OAuthToken, error) {
	c := *cfg
	c.ClientID = firstClientID(cfg.ClientID)
	tok, err := exchangeOAuthCode(a.tokenURL, &c, code)
	if err != nil {
		return nil, err
	}
	if _, err := parseAppleIDToken(tok.IDToken); err != nil {
		return nil, err
	}
	if _, err := oidc.Verify("apple", tok.IDToken, []string{c.ClientID}, ""); err != nil {
		return nil, fmt.Errorf("apple id_token: %w", err)
	}
	return tok, nil
}

func (appleProvider) FetchUser(tok *OAuthToken, callback url.Values) (*OAuthUserInfo, error) {
	// ExchangeCode already verified the signature, iss, aud and exp.
	claims, err := parseAppleIDToken(tok.IDToken)
	if err != nil {
		return nil, err
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, errors.New("apple id_token has no sub")
	}
	email, _ := claims["email"].(string)
	return &OAuthUserInfo{ProviderID: sub, Email: email,
		EmailVerified: EmailVerifiedClaim(claims["email_verified"]), Name: appleName(callback.Get("user"))}, nil
}

func parseAppleIDToken(raw string) (jwt.MapClaims, error) {
	if raw == "" {
		return nil, errors.New("apple response has no id_token")
	}
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(raw, claims); err != nil {
		return nil, err
	}
	if iss, _ := claims["iss"].(string); iss != appleIssuer {
		return nil, errors.New("apple id_token issuer mismatch")
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil || !exp.After(time.Now()) {
		return nil, errors.New("apple id_token expired or has no exp")
	}
	return claims, nil
}

func appleName(raw string) string {
	if raw == "" || len(raw) > maxAppleUserJSON {
		return ""
	}
	var u struct {
		Name struct {
			FirstName string `json:"firstName"`
			LastName  string `json:"lastName"`
		} `json:"name"`
	}
	if json.Unmarshal([]byte(raw), &u) != nil {
		return ""
	}
	return strings.TrimSpace(u.Name.FirstName + " " + u.Name.LastName)
}
