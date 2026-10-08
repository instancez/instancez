package vet

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

var secretEnvName = regexp.MustCompile(`(?i)password|secret|token|key|credential|private|pass`)

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
		for _, k := range secretEnvKeys(c.cfg.Functions[name].Env, true) {
			c.hardcoded([]any{"functions", name, "env", k}, fmt.Sprintf("Function %s env %s", name, k))
		}
	}
}

// secretEnvKeys returns the sorted secret-looking env keys, only literal ones when literalOnly.
func secretEnvKeys(env map[string]string, literalOnly bool) []string {
	return slices.DeleteFunc(slices.Sorted(maps.Keys(env)), func(k string) bool {
		return !secretEnvName.MatchString(k) || (literalOnly && !isLiteral(env[k]))
	})
}

func ruleFunctionSecrets(c *ctx) {
	for _, name := range slices.Sorted(maps.Keys(c.cfg.Functions)) {
		fn := c.cfg.Functions[name]
		if keys := secretEnvKeys(fn.Env, false); !fn.AuthRequired && len(keys) > 0 {
			c.add("function-public-secrets", Medium, []any{"functions", name, "auth_required"},
				"Public function can use secrets",
				fmt.Sprintf("Function %s can be called without signing in and has secret-looking env %s in scope.", name, keys[0]),
				"Set auth_required: true, or make sure the function never exposes what that value unlocks.")
		}
	}
}
