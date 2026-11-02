package config

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// typeAliases maps common wrong type names to the Postgres type users meant.
var typeAliases = map[string]string{"string": "text", "str": "text", "datetime": "timestamptz", "number": "numeric", "long": "bigint", "object": "jsonb", "dict": "jsonb", "blob": "bytea"}

func suggestType(base string) string {
	if a, ok := typeAliases[base]; ok {
		return a
	}
	// longer names first: a typo more often drops letters than adds them
	names := slices.SortedFunc(maps.Keys(validPostgresTypes), func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return closest(base, names)
}

func didYouMean(s string) string {
	if s == "" {
		return ""
	}
	return fmt.Sprintf("Did you mean %q?", s)
}

// yamlStructTypes resolves the Go type names yaml.v3 reports to their structs.
var yamlStructTypes = map[string]reflect.Type{
	"domain.Config": reflect.TypeOf(domain.Config{}), "domain.Auth": reflect.TypeOf(domain.Auth{}),
	"domain.AuthEmail": reflect.TypeOf(domain.AuthEmail{}), "domain.EmailTemplate": reflect.TypeOf(domain.EmailTemplate{}),
	"domain.OAuthProvider": reflect.TypeOf(domain.OAuthProvider{}), "domain.Server": reflect.TypeOf(domain.Server{}),
	"domain.Project": reflect.TypeOf(domain.Project{}), "domain.ProjectCloud": reflect.TypeOf(domain.ProjectCloud{}),
	"domain.Providers": reflect.TypeOf(domain.Providers{}), "domain.EmailProvider": reflect.TypeOf(domain.EmailProvider{}),
	"domain.StorageProvider": reflect.TypeOf(domain.StorageProvider{}), "domain.DatabaseConfig": reflect.TypeOf(domain.DatabaseConfig{}),
	"domain.Table": reflect.TypeOf(domain.Table{}), "domain.Field": reflect.TypeOf(domain.Field{}),
	"domain.Index": reflect.TypeOf(domain.Index{}), "domain.RLSPolicy": reflect.TypeOf(domain.RLSPolicy{}),
	"domain.Function": reflect.TypeOf(domain.Function{}), "domain.CodeFunction": reflect.TypeOf(domain.CodeFunction{}),
	"domain.Bucket": reflect.TypeOf(domain.Bucket{}), "domain.ForeignKey": reflect.TypeOf(domain.ForeignKey{}),
}

// yamlKeys lists the yaml key names of a struct type, in field order.
func yamlKeys(t reflect.Type) []string {
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	return keys
}

func suggestKey(goType, key string) string {
	t, ok := yamlStructTypes[goType]
	if !ok {
		return ""
	}
	return didYouMean(closest(key, yamlKeys(t)))
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// closest returns the nearest candidate within edit distance 2, or "" if none.
func closest(input string, candidates []string) string {
	if input == "" {
		return ""
	}
	best, bestDist := "", 3
	for _, c := range candidates {
		d := levenshtein(strings.ToLower(input), strings.ToLower(c))
		if d < bestDist && d < len([]rune(input)) {
			best, bestDist = c, d
		}
	}
	return best
}
