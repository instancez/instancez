package config

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/instancez/instancez/internal/domain"
	"gopkg.in/yaml.v3"
)

// Location is a config path with its position in the YAML source.
type Location struct {
	Path         string
	Line, Column int
}

var pathSegRe = regexp.MustCompile(`^([^\[\]]*)((?:\[\d+\])*)$`)

const envPlaceholder = "__INZENV_"

// rootMapping parses raw; a ${VAR} inside flow style is not valid YAML, so retry with placeholders.
func rootMapping(raw []byte) *yaml.Node {
	if len(raw) == 0 {
		return nil
	}
	var doc yaml.Node
	if yaml.Unmarshal(raw, &doc) != nil {
		safe := envVarPattern.ReplaceAllString(string(raw), envPlaceholder+"${1}__")
		doc = yaml.Node{}
		if yaml.Unmarshal([]byte(safe), &doc) != nil {
			return nil
		}
	}
	if len(doc.Content) == 0 {
		return nil
	}
	return doc.Content[0]
}

// Annotate fills Line and Column on errs that have none, using the raw (pre-interpolation) YAML.
func Annotate(raw []byte, errs domain.ValidationErrors) {
	root := rootMapping(raw)
	if root == nil {
		return
	}
	for _, e := range errs {
		if e == nil || e.Line > 0 {
			continue
		}
		e.Line, e.Column = locatePath(root, e.Path)
	}
}

// locatePath returns the position of the deepest resolvable prefix of path, or 0,0.
func locatePath(root *yaml.Node, path string) (line, col int) {
	if path == "" || strings.ContainsAny(path, "$<>() ") {
		return 0, 0
	}
	node := root
	for _, seg := range strings.Split(path, ".") {
		m := pathSegRe.FindStringSubmatch(seg)
		if m == nil {
			return line, col
		}
		if m[1] != "" {
			next, pos := childByKey(node, m[1])
			if next == nil {
				return line, col
			}
			node, line, col = next, pos.Line, pos.Column
		}
		for _, idx := range regexp.MustCompile(`\d+`).FindAllString(m[2], -1) {
			i, _ := strconv.Atoi(idx)
			if node.Kind != yaml.SequenceNode || i >= len(node.Content) {
				return line, col
			}
			node = node.Content[i]
			line, col = node.Line, node.Column
		}
	}
	return line, col
}

// childByKey looks up key in a mapping, or the element named key in a sequence.
func childByKey(node *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				return node.Content[i+1], node.Content[i]
			}
		}
	case yaml.SequenceNode:
		for _, el := range node.Content {
			if el.Kind != yaml.MappingNode {
				continue
			}
			if v, _ := childByKey(el, "name"); v != nil && v.Value == key {
				return el, el
			}
		}
	}
	return nil, nil
}

var envRefInScalar = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-.*?)?\}|` + envPlaceholder + `([A-Za-z_][A-Za-z0-9_]*)__`)

// EnvRefLocations maps each ${NAME} to the scalars that reference it.
func EnvRefLocations(raw []byte) map[string][]Location {
	out := map[string][]Location{}
	root := rootMapping(raw)
	if root == nil {
		return out
	}
	var walk func(n *yaml.Node, path string)
	walk = func(n *yaml.Node, path string) {
		switch n.Kind {
		case yaml.ScalarNode:
			for _, m := range envRefInScalar.FindAllStringSubmatch(n.Value, -1) {
				name := m[1] + m[2]
				out[name] = append(out[name], Location{Path: path, Line: n.Line, Column: n.Column})
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				walk(n.Content[i+1], joinPath(path, n.Content[i].Value))
			}
		case yaml.SequenceNode:
			for i, el := range n.Content {
				walk(el, seqPath(path, i, el))
			}
		}
	}
	walk(root, "")
	return out
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// seqPath names fields by their name (matching validate paths) and everything else by index.
func seqPath(parent string, i int, el *yaml.Node) string {
	if strings.HasSuffix(parent, "fields") && el.Kind == yaml.MappingNode {
		if v, _ := childByKey(el, "name"); v != nil && v.Value != "" {
			return parent + "." + v.Value
		}
	}
	return parent + "[" + strconv.Itoa(i) + "]"
}

// keyParentPath finds the path of the mapping that holds key on line.
func keyParentPath(root *yaml.Node, line int, key string) (path string, col int, ok bool) {
	var walk func(n *yaml.Node, p string) bool
	walk = func(n *yaml.Node, p string) bool {
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i]
				if k.Line == line && k.Value == key {
					path, col = p, k.Column
					return true
				}
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				if walk(n.Content[i+1], joinPath(p, n.Content[i].Value)) {
					return true
				}
			}
		case yaml.SequenceNode:
			for i, el := range n.Content {
				if walk(el, seqPath(p, i, el)) {
					return true
				}
			}
		}
		return false
	}
	ok = walk(root, "")
	return
}

// valuePathAt finds the path of the value node that starts on line and, for scalars, holds val.
func valuePathAt(root *yaml.Node, line int, val string) (path string, col int, ok bool) {
	var walk func(n *yaml.Node, p string) bool
	walk = func(n *yaml.Node, p string) bool {
		if p != "" && n.Line == line && ((val == "" && n.Kind != yaml.ScalarNode) || (val != "" && n.Kind == yaml.ScalarNode && scalarMatches(n.Value, val))) {
			path, col = p, n.Column
			return true
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if walk(n.Content[i+1], joinPath(p, n.Content[i].Value)) {
					return true
				}
			}
		case yaml.SequenceNode:
			for i, el := range n.Content {
				if walk(el, seqPath(p, i, el)) {
					return true
				}
			}
		}
		return false
	}
	ok = walk(root, "")
	return
}

// scalarMatches handles yaml.v3 truncating long values in its messages with "...".
func scalarMatches(actual, quoted string) bool {
	if p, ok := strings.CutSuffix(quoted, "..."); ok {
		return strings.HasPrefix(actual, p)
	}
	return actual == quoted
}
