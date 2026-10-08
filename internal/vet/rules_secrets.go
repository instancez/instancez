package vet

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

const secretFix = "Move it to an environment variable and reference it as ${INSTANCEZ_ENV_NAME}."

func isLiteral(s string) bool { return s != "" && !strings.Contains(s, "${") }

func (c *ctx) hardcoded(path []any, what string) {
	c.add("hardcoded-secret", High, path, "Secret written in the config",
		fmt.Sprintf("%s is a literal value, so it ships with the file and lands in version control.", what), secretFix)
}

func ruleHardcodedSecret(c *ctx) {
	p := c.cfg.Providers
	if p.Email != nil && isLiteral(p.Email.APIKey) {
		c.hardcoded([]any{"providers", "email", "api_key"}, "The email api_key")
	}
	if p.Storage != nil {
		if isLiteral(p.Storage.AccessKeyID) {
			c.hardcoded([]any{"providers", "storage", "access_key_id"}, "The storage access_key_id")
		}
		if isLiteral(p.Storage.SecretAccessKey) {
			c.hardcoded([]any{"providers", "storage", "secret_access_key"}, "The storage secret_access_key")
		}
	}
	if a := c.cfg.Auth; a != nil {
		for _, name := range slices.Sorted(maps.Keys(a.OAuth)) {
			if o := a.OAuth[name]; o != nil && isLiteral(o.ClientSecret) {
				c.hardcoded([]any{"auth", "oauth", name, "client_secret"}, "The "+name+" OAuth client_secret")
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.cfg.Functions)) {
		env := c.cfg.Functions[name].Env
		for _, k := range slices.Sorted(maps.Keys(env)) {
			if secretEnvName.MatchString(k) && isLiteral(env[k]) {
				c.hardcoded([]any{"functions", name, "env", k}, fmt.Sprintf("Function %s env %s", name, k))
			}
		}
	}
}
