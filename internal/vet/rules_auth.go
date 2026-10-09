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
	c.withEdit(Edit{Path: []any{"auth", "jwt_expiry"}, Value: "1h"})
}

func ruleSignupUnverified(c *ctx) {
	a := c.cfg.Auth
	if a == nil || !a.SignupAllowed() || (a.Email != nil && a.Email.VerifyEmail) {
		return
	}
	if c.cfg.Providers.Email == nil {
		c.add("signup-unverified-email", Low, []any{"auth", "email", "verify_email"}, "Sign-up does not verify email",
			"Anyone can sign up with an email address they do not own, and no email provider is configured to send verification mail.",
			"Configure providers.email, then set auth.email.verify_email: true, or set auth.allow_signup: false.")
		return
	}
	c.add("signup-unverified-email", Medium, []any{"auth", "email", "verify_email"}, "Sign-up does not verify email",
		"Anyone can sign up with an email address they do not own.",
		"Set auth.email.verify_email: true, or set auth.allow_signup: false.")
	c.withEdit(Edit{Path: []any{"auth", "email", "verify_email"}, Value: true})
}

func ruleAnonymousSignins(c *ctx) {
	if a := c.cfg.Auth; a != nil && a.AnonymousAllowed() {
		c.add("anonymous-signins", Low, []any{"auth", "allow_anonymous"}, "Anonymous sign-ins are on",
			"Anyone can get a signed-in session without credentials, which passes any auth.uid() is not null policy.",
			"Turn off auth.allow_anonymous unless you need guest users, and scope policies to real accounts.")
		c.withEdit(Edit{Path: []any{"auth", "allow_anonymous"}, Value: false})
	}
}

var scriptSchemes = []string{"javascript", "data", "vbscript"}

func (c *ctx) checkRedirect(path []any, raw string) {
	if raw == "" {
		return
	}
	sev, msg := Low, fmt.Sprintf("%q uses a wildcard, so more hosts than you intend may receive session tokens.", raw)
	if !strings.Contains(raw, "*") {
		u, err := url.Parse(raw)
		switch {
		case err != nil:
			return
		case slices.Contains(scriptSchemes, u.Scheme):
			sev, msg = Medium, fmt.Sprintf("%q is a %s: URL, which can run script instead of returning to your app.", raw, u.Scheme)
		case u.Scheme != "http":
			return
		case slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, strings.ToLower(u.Hostname())):
			msg = fmt.Sprintf("%q is a local http URL; remove it before going to production.", raw)
		default:
			sev, msg = Medium, fmt.Sprintf("%q is plain http, so session tokens in the redirect can be read on the network.", raw)
		}
	}
	c.add("redirect-insecure", sev, path, "Risky redirect URL", msg, "Use an exact https:// URL without wildcards.")
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

func ruleCORS(c *ctx) {
	for i, o := range c.cfg.Server.CORS.Origins {
		switch strings.ToLower(o) {
		case "*":
			c.add("cors-wildcard", Low, []any{"server", "cors", "origins", i}, "CORS allows every origin",
				"Any website can call this API from a browser.",
				"List only your own site origins in server.cors.origins.")
		case "null":
			c.add("cors-null-origin", Medium, []any{"server", "cors", "origins", i}, "CORS allows the null origin",
				"Sandboxed iframes and local files send the null origin, so attackers can use them to call this API.",
				"Remove \"null\" from server.cors.origins.")
			c.withEdit(Edit{Path: []any{"server", "cors", "origins", i}, Value: "null", Remove: true})
		}
	}
}

func ruleMaxLimit(c *ctx) {
	if c.cfg.Server.MaxLimit == -1 {
		c.add("max-limit-disabled", Medium, []any{"server", "max_limit"}, "Row limit is off",
			"One request can pull a whole table, which makes scraping and memory spikes easy.",
			"Set server.max_limit to a number such as 1000.")
		c.withEdit(Edit{Path: []any{"server", "max_limit"}, Value: 1000})
	}
}
