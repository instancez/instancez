package cloud

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ReadProjectID extracts project.cloud.project_id from a YAML document.
// Returns "" if the field is not present. Never modifies the input.
func ReadProjectID(src []byte) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return "", fmt.Errorf("parse yaml: %w", err)
	}
	if len(root.Content) == 0 {
		return "", nil
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return "", nil
	}
	proj := findMapValue(doc, "project")
	if proj == nil {
		return "", nil
	}
	cloud := findMapValue(proj, "cloud")
	if cloud == nil {
		return "", nil
	}
	v := findMapValue(cloud, "project_id")
	if v == nil || v.Kind != yaml.ScalarNode {
		return "", nil
	}
	return v.Value, nil
}

// WriteProjectID sets project.cloud.project_id to the given value. It edits
// the source text in place so the rest of the file (comments, indentation,
// ordering) stays byte-identical; unusual layouts (flow style, quoted ids)
// fall back to re-encoding the document.
func WriteProjectID(src []byte, projectID string) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if len(root.Content) == 0 {
		return nil, errors.New("empty yaml document")
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, errors.New("top-level yaml must be a mapping")
	}
	if out, ok := writeProjectIDInPlace(string(src), doc, projectID); ok && validProjectIDOutput(out, projectID) {
		return []byte(out), nil
	}
	return writeProjectIDReencode(&root, doc, projectID)
}

// validProjectIDOutput re-parses an in-place edit so a bad edit falls back to re-encoding.
func validProjectIDOutput(out, projectID string) bool {
	got, err := ReadProjectID([]byte(out))
	return err == nil && got == projectID
}

func isBlockMapping(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.MappingNode && n.Style&yaml.FlowStyle == 0 && len(n.Content) > 0 && n.Content[0].Line > 0
}

func findMapEntry(m *yaml.Node, key string) (k, v *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

func writeProjectIDInPlace(src string, doc *yaml.Node, projectID string) (string, bool) {
	val, err := yaml.Marshal(projectID)
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(string(val))
	if strings.Contains(src, "\r\n") || strings.Contains(src, "\n---") || strings.HasPrefix(src, "---\n") && strings.Count(src, "\n---") > 0 {
		return "", false
	}
	lines := strings.SplitAfter(src, "\n")
	insertBefore := func(line int, add ...string) string {
		out := append([]string{}, lines[:line-1]...)
		for _, a := range add {
			out = append(out, a+"\n")
		}
		return strings.Join(append(out, lines[line-1:]...), "")
	}

	projKey, proj := findMapEntry(doc, "project")
	if projKey == nil {
		if doc.Column != 1 || doc.Style&yaml.FlowStyle != 0 {
			return "", false
		}
		sep := ""
		if !strings.HasSuffix(src, "\n") {
			sep = "\n"
		}
		return src + sep + "project:\n  cloud:\n    project_id: " + value + "\n", true
	}
	if !isBlockMapping(proj) {
		return "", false
	}
	childIndent := proj.Content[0].Column - 1
	step := childIndent - (projKey.Column - 1)
	if step <= 0 {
		return "", false
	}
	pad := strings.Repeat(" ", childIndent)
	cloudKey, cloud := findMapEntry(proj, "cloud")
	if cloudKey == nil {
		return insertBefore(proj.Content[0].Line, pad+"cloud:", pad+strings.Repeat(" ", step)+"project_id: "+value), true
	}
	if !isBlockMapping(cloud) {
		return "", false
	}
	pidKey, pid := findMapEntry(cloud, "project_id")
	if pidKey == nil {
		return insertBefore(cloud.Content[0].Line, strings.Repeat(" ", cloud.Content[0].Column-1)+"project_id: "+value), true
	}
	if pid.Kind != yaml.ScalarNode || pid.Style != 0 || pid.Value == "" || pid.Line < 1 || pid.Line > len(lines) {
		return "", false
	}
	line := lines[pid.Line-1]
	start := pid.Column - 1
	if start < 0 || start+len(pid.Value) > len(line) || line[start:start+len(pid.Value)] != pid.Value {
		return "", false
	}
	lines[pid.Line-1] = line[:start] + value + line[start+len(pid.Value):]
	return strings.Join(lines, ""), true
}

func writeProjectIDReencode(root, doc *yaml.Node, projectID string) ([]byte, error) {
	proj := findMapValue(doc, "project")
	if proj == nil {
		proj = &yaml.Node{Kind: yaml.MappingNode}
		appendMapEntry(doc, "project", proj)
	}

	cloud := findMapValue(proj, "cloud")
	if cloud == nil {
		cloud = &yaml.Node{Kind: yaml.MappingNode}
		appendMapEntry(proj, "cloud", cloud)
	}

	pid := findMapValue(cloud, "project_id")
	if pid == nil {
		appendMapEntry(cloud, "project_id", &yaml.Node{Kind: yaml.ScalarNode, Value: projectID, Tag: "!!str"})
	} else {
		pid.Kind = yaml.ScalarNode
		pid.Value = projectID
		pid.Tag = "!!str"
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}
	return out, nil
}

// findMapValue returns the value node for the given key in a MappingNode,
// or nil if the key is absent.
func findMapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// appendMapEntry adds a key/value pair to a MappingNode.
func appendMapEntry(m *yaml.Node, key string, value *yaml.Node) {
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		value,
	)
}
