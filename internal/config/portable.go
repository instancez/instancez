package config

import (
	"bytes"
	"slices"

	"gopkg.in/yaml.v3"
)

// ReadExpandedConfigFile is ReadConfigFile plus shorthand expansion, for platforms that may not know the shorthand keys. Plain configs come back byte-identical.
func ReadExpandedConfigFile(path string) ([]byte, error) {
	data, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	out, _, err := ExpandYAML(data)
	return out, err
}

// ExpandYAML rewrites timestamps and owner shorthands into the long form in the YAML itself, leaving everything else as written. Conflicting shorthands are left for validation to report. It returns data unchanged when nothing expanded.
func ExpandYAML(data []byte) ([]byte, bool, error) {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return data, false, nil
	}
	root := rootMapping(&doc)
	changed := false
	if _, tables := mappingEntry(root, "tables"); tables != nil && tables.Kind == yaml.MappingNode {
		for i := 1; i < len(tables.Content); i += 2 {
			if t := tables.Content[i]; t.Kind == yaml.MappingNode {
				changed = expandTableNode(t) || changed
			}
		}
	}
	if _, storage := mappingEntry(root, "storage"); storage != nil && storage.Kind == yaml.MappingNode {
		for i := 1; i < len(storage.Content); i += 2 {
			if b := storage.Content[i]; b.Kind == yaml.MappingNode {
				changed = expandRLSNodes(b, true) || changed
			}
		}
	}
	if !changed {
		return data, false, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, false, err
	}
	_ = enc.Close()
	return buf.Bytes(), true, nil
}

const timestampFields = `[{name: created_at, type: timestamptz, required: true, default: now()}, {name: updated_at, type: timestamptz, required: true, default: now(), auto_updated_at: true}]`

func removeEntry(m *yaml.Node, idx int) { m.Content = slices.Delete(m.Content, idx, idx+2) }

func expandTableNode(t *yaml.Node) bool {
	changed := expandRLSNodes(t, false)
	idx, ts := mappingEntry(t, "timestamps")
	if ts == nil || ts.Kind != yaml.ScalarNode {
		return changed
	}
	if ts.Value != "true" {
		if ts.Value == "false" {
			removeEntry(t, idx)
			return true
		}
		return changed
	}
	_, fields := mappingEntry(t, "fields")
	if fields != nil && fields.Kind != yaml.SequenceNode {
		return changed
	}
	if fields != nil {
		for _, f := range fields.Content {
			if _, n := mappingEntry(f, "name"); n != nil && (n.Value == "created_at" || n.Value == "updated_at") {
				return changed
			}
		}
	}
	var add yaml.Node
	if yaml.Unmarshal([]byte(timestampFields), &add) != nil {
		return changed
	}
	if fields == nil {
		fields = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		t.Content = append(t.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "fields"}, fields)
	}
	fields.Content = append(fields.Content, add.Content[0].Content...)
	idx, _ = mappingEntry(t, "timestamps")
	removeEntry(t, idx)
	return true
}

func expandRLSNodes(parent *yaml.Node, bucket bool) bool {
	_, rls := mappingEntry(parent, "rls")
	if rls == nil || rls.Kind != yaml.SequenceNode {
		return false
	}
	changed := false
	for _, p := range rls.Content {
		if p.Kind == yaml.MappingNode {
			changed = expandOwnerNode(p, bucket) || changed
		}
	}
	return changed
}

func scalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

func expandOwnerNode(p *yaml.Node, bucket bool) bool {
	oi, owner := mappingEntry(p, "owner")
	if owner == nil || owner.Kind != yaml.ScalarNode || !identRE.MatchString(owner.Value) ||
		bucket && owner.Value != "uploaded_by" && owner.Value != "owner" {
		return false
	}
	if _, u := mappingEntry(p, "using"); u != nil {
		return false
	}
	if _, w := mappingEntry(p, "with_check"); w != nil {
		return false
	}
	ops := allRLSOps
	if _, o := mappingEntry(p, "operations"); o != nil {
		if o.Kind != yaml.SequenceNode {
			return false
		}
		ops = ops[:0:0]
		for _, n := range o.Content {
			ops = append(ops, n.Value)
		}
	} else {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, op := range allRLSOps {
			seq.Content = append(seq.Content, scalarNode(op))
		}
		p.Content = append(p.Content, scalarNode("operations"), seq)
	}
	expr := owner.Value + " = auth.uid()"
	if slices.Contains(ops, "select") || slices.Contains(ops, "update") || slices.Contains(ops, "delete") {
		p.Content = append(p.Content, scalarNode("using"), scalarNode(expr))
	}
	if slices.Contains(ops, "insert") || slices.Contains(ops, "update") {
		p.Content = append(p.Content, scalarNode("with_check"), scalarNode(expr))
	}
	removeEntry(p, oi)
	return true
}
