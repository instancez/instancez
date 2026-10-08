package vet

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// formatPath renders a path of string keys and int indexes as tables.posts.rls[1].using.
func formatPath(path []any) string {
	var b strings.Builder
	for _, p := range path {
		switch v := p.(type) {
		case int:
			fmt.Fprintf(&b, "[%d]", v)
		default:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			fmt.Fprint(&b, v)
		}
	}
	return b.String()
}

// lineOf returns the 1-based line of path, the nearest existing parent when it is
// missing, or 0 when nothing resolves.
func lineOf(doc *yaml.Node, path []any) int {
	if doc == nil {
		return 0
	}
	node := doc
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return 0
		}
		node = node.Content[0]
	}
	line := 0
	for _, p := range path {
		next, at := child(node, p)
		if next == nil {
			break
		}
		node, line = next, at
	}
	return line
}

// child returns the child for key (string) or index (int) and the line to report for it.
func child(n *yaml.Node, key any) (*yaml.Node, int) {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	switch k := key.(type) {
	case string:
		if n.Kind != yaml.MappingNode {
			return nil, 0
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == k {
				return n.Content[i+1], n.Content[i].Line
			}
		}
	case int:
		if n.Kind == yaml.SequenceNode && k >= 0 && k < len(n.Content) {
			return n.Content[k], n.Content[k].Line
		}
	}
	return nil, 0
}
