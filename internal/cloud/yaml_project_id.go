package cloud

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ReadProjectID extracts project.cloud.project_id from a YAML document.
// Returns "" if the field is not present. Never modifies the input.
func ReadProjectID(src []byte) (string, error) {
	return readCloudScalar(src, "project_id")
}

// ReadAPIURL extracts project.cloud.api_url from a YAML document. Returns
// "" if the field is not present. Used by APIURLFromConfig to allow a
// project to pin its own cloud endpoint (overrides INSTANCEZ_CLOUD_API).
func ReadAPIURL(src []byte) (string, error) {
	return readCloudScalar(src, "api_url")
}

// readCloudScalar pulls a scalar value out of project.cloud.<key>.
func readCloudScalar(src []byte, key string) (string, error) {
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
	v := findMapValue(cloud, key)
	if v == nil || v.Kind != yaml.ScalarNode {
		return "", nil
	}
	return v.Value, nil
}

// WriteProjectID sets project.cloud.project_id to the given value. Creates
// the cloud subtree if missing. Returns the rewritten YAML bytes.
//
// Preserves the document's existing structure as much as yaml.v3 supports;
// comment preservation depends on node ordering and may not be perfect.
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

	out, err := yaml.Marshal(&root)
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
