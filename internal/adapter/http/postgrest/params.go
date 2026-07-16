package postgrest

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/instancez/instancez/internal/domain"
)

// ParseEmbedScopedParams routes "<embed>.*" query parameters into the
// corresponding Embed's Where/Order/Limit/Offset fields. The embed table is
// used to validate columns. Order, Limit, and Offset are only allowed for
// has-many (reverse) embeds — belongs-to embeds are joined to a single row,
// so ordering or limiting the join has no meaning.
func ParseEmbedScopedParams(vals url.Values, embedByName map[string]*Embed, allTables map[string]domain.Table) error {
	for key, values := range vals {
		for embName, emb := range embedByName {
			prefix := embName + "."
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			suffix := strings.TrimPrefix(key, prefix)
			embTable, ok := allTables[emb.RefTable]
			if !ok {
				return fmt.Errorf("embed %q references unknown table %q", embName, emb.RefTable)
			}
			// Embed-scoped filters and order may reference select-list
			// aliases (explicit or aggregate defaults) in the embed's own
			// projection, e.g. `posts.count.gt=0` against `posts(id.count())`.
			aliasSet := CollectSelectAliases(emb.Columns)
			embValidate := func(col string) error {
				if _, ok := aliasSet[col]; ok {
					return nil
				}
				return ValidateColumn(embTable, col)
			}
			switch suffix {
			case "or", "and":
				for _, v := range values {
					node, err := parseLogicListWith(suffix, v, embValidate)
					if err != nil {
						return fmt.Errorf("invalid %s for embed %q: %w", suffix, embName, err)
					}
					if emb.Where == nil {
						emb.Where = &WhereNode{Op: "and"}
					}
					emb.Where.Children = append(emb.Where.Children, node)
				}
			case "order":
				if !emb.IsReverse {
					return fmt.Errorf("order not allowed on belongs-to embed %q", embName)
				}
				for _, v := range values {
					clauses, err := ParseOrderValueWithSelect(v, embTable, emb.Columns)
					if err != nil {
						return fmt.Errorf("invalid order for embed %q: %w", embName, err)
					}
					emb.Order = append(emb.Order, clauses...)
				}
			case "limit":
				if !emb.IsReverse {
					return fmt.Errorf("limit not allowed on belongs-to embed %q", embName)
				}
				if len(values) == 0 {
					break
				}
				n, err := strconv.Atoi(values[0])
				if err != nil || n < 0 {
					return fmt.Errorf("invalid limit for embed %q: %s", embName, values[0])
				}
				emb.Limit = &n
			case "offset":
				if !emb.IsReverse {
					return fmt.Errorf("offset not allowed on belongs-to embed %q", embName)
				}
				if len(values) == 0 {
					break
				}
				n, err := strconv.Atoi(values[0])
				if err != nil || n < 0 {
					return fmt.Errorf("invalid offset for embed %q: %s", embName, values[0])
				}
				emb.Offset = &n
			case "select":
				// Reserved name inside an embed scope: column projection is
				// controlled by the parentheses syntax (posts(col1,col2))
				// rather than a separate posts.select= param.
				return fmt.Errorf("embed param %q.%s not supported", embName, suffix)
			default:
				if err := ValidateColumn(embTable, suffix); err != nil {
					return fmt.Errorf("invalid filter on embed %q: %w", embName, err)
				}
				for _, v := range values {
					leaf, err := parseLeafValue(suffix, v)
					if err != nil {
						return fmt.Errorf("invalid filter on embed %q: %w", embName, err)
					}
					if emb.Where == nil {
						emb.Where = &WhereNode{Op: "and"}
					}
					emb.Where.Children = append(emb.Where.Children, leaf)
				}
			}
			break
		}
	}
	return nil
}
