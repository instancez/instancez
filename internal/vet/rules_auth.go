package vet

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"
)

func ruleJWTExpiry(c *ctx) {
	a := c.cfg.Auth
	if a == nil || strings.Contains(a.JWTExpiry, "${") {
		return
	}
	d, err := time.ParseDuration(a.JWTExpiry)
	if err != nil || d <= time.Hour {
		return
	}
	sev := Medium
	if d > 24*time.Hour {
		sev = High
	}
	c.add("jwt-expiry-long", sev, []any{"auth", "jwt_expiry"}, "Access tokens live too long",
		fmt.Sprintf("jwt_expiry is %s, and a leaked token stays valid that long.", a.JWTExpiry),
		"Use 1h or less; refresh tokens keep users signed in.")
}

func ruleSignupUnverified(c *ctx) {
	a := c.cfg.Auth
	if a == nil || !a.SignupAllowed() || (a.Email != nil && a.Email.VerifyEmail) {
		return
	}
	c.add("signup-unverified-email", Medium, []any{"auth", "email", "verify_email"}, "Sign-up does not verify email",
		"Anyone can sign up with an email address they do not own.",
		"Set auth.email.verify_email: true, or set auth.allow_signup: false.")
}

func ruleAnonymousSignins(c *ctx) {
	if a := c.cfg.Auth; a != nil && a.AnonymousAllowed() {
		c.add("anonymous-signins", Low, []any{"auth", "allow_anonymous"}, "Anonymous sign-ins are on",
			"Anyone can get a signed-in session without credentials, which passes any auth.uid() is not null policy.",
			"Turn off auth.allow_anonymous unless you need guest users, and scope policies to real accounts.")
	}
}

func (c *ctx) checkRedirect(path []any, raw string) {
	if raw == "" {
		return
	}
	title := "Risky redirect URL"
	fix := "Use an exact https:// URL without wildcards."
	if strings.Contains(raw, "*") {
		c.add("redirect-insecure", Low, path, title,
			fmt.Sprintf("%q uses a wildcard, so more hosts than you intend may receive session tokens.", raw), fix)
		return
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return
	}
	if h := u.Hostname(); h == "localhost" || h == "127.0.0.1" {
		c.add("redirect-insecure", Low, path, title,
			fmt.Sprintf("%q is a local http URL; remove it before going to production.", raw), fix)
		return
	}
	c.add("redirect-insecure", Medium, path, title,
		fmt.Sprintf("%q is plain http, so session tokens in the redirect can be read on the network.", raw), fix)
}

func ruleRedirects(c *ctx) {
	a := c.cfg.Auth
	if a == nil {
		return
	}
	for i, r := range a.RedirectURLs {
		c.checkRedirect([]any{"auth", "redirect_urls", i}, r)
	}
	for _, name := range slices.Sorted(maps.Keys(a.OAuth)) {
		if o := a.OAuth[name]; o != nil {
			c.checkRedirect([]any{"auth", "oauth", name, "redirect_url"}, o.RedirectURL)
		}
	}
}
