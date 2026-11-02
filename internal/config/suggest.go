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

// extensionTypes are real Postgres types outside the allowlist; suggesting a core type for them misleads.
var extensionTypes = map[string]bool{"vector": true, "citext": true, "hstore": true, "ltree": true, "geometry": true, "geography": true}

var sortedTypeNames = slices.Sorted(maps.Keys(validPostgresTypes))

func suggestType(base string) string {
	if a, ok := typeAliases[base]; ok {
		return a
	}
	if extensionTypes[base] {
		return ""
	}
	return closest(base, sortedTypeNames)
}

func didYouMean(s string) string {
	if s == "" {
		return ""
	}
	return fmt.Sprintf("Did you mean %q?", s)
}

// yamlStructTypes maps the type names yaml.v3 reports (domain.Auth) to the structs reachable from domain.Config.
var yamlStructTypes = func() map[string]reflect.Type {
	out := map[string]reflect.Type{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || out[t.String()] != nil {
			return
		}
		out[t.String()] = t
		for i := 0; i < t.NumField(); i++ {
			walk(t.Field(i).Type)
		}
	}
	walk(reflect.TypeOf(domain.Config{}))
	return out
}()

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

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// closest returns the nearest candidate within edit distance 2, or "" if none.
func closest(input string, candidates []string) string {
	if input == "" {
		return ""
	}
	in := strings.ToLower(input)
	best, bestDist, bestPrefix := "", 3, -1
	for _, c := range candidates {
		lc := strings.ToLower(c)
		d := levenshtein(in, lc)
		if d >= bestDist+1 || d >= len([]rune(in)) {
			continue
		}
		// ties: longer shared prefix, then the longer name (typos drop letters more than they add them)
		p := commonPrefix(in, lc)
		if d < bestDist || p > bestPrefix || (p == bestPrefix && len(c) > len(best)) {
			best, bestDist, bestPrefix = c, d, p
		}
	}
	return best
}
