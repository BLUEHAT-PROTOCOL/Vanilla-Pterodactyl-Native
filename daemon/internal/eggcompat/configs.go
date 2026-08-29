package eggcompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Supported parsers (wings parity): file, yaml, properties, ini, json, xml.
// XML falls back to literal replacement; the rest are structured.

// ApplyEggConfig processes one config entry with the given parser.
// replaces: [{match, if_value, replace_with}]; find: legacy map (key -> value(s)).
func ApplyEggConfig(dataDir, file, parser string, replaces []map[string]string, find map[string]interface{}) error {
	if file == "" {
		return fmt.Errorf("config file path empty")
	}
	f := strings.ReplaceAll(file, "/home/container", dataDir)
	f = strings.ReplaceAll(f, "/mnt/server", dataDir)
	f = strings.TrimPrefix(f, dataDir+"/")
	target := filepath.Join(dataDir, filepath.FromSlash(f))

	if _, err := os.Stat(target); err != nil {
		return nil // nothing to process
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	content := string(data)

	switch strings.ToLower(parser) {
	case "yaml", "yml":
		out, err := applyYAML(content, find)
		if err != nil {
			return err
		}
		content = out
	case "properties", "ini":
		out, err := applyKeyValue(content, find, parser == "ini")
		if err != nil {
			return err
		}
		content = out
	case "json":
		out, err := applyJSON(content, find)
		if err != nil {
			return err
		}
		content = out
	default:
		// "file" and "xml": literal replacement
	}

	for _, rep := range replaces {
		match := strings.ReplaceAll(rep["match"], "/home/container", dataDir)
		replaceWith := strings.ReplaceAll(rep["replace_with"], "/home/container", dataDir)
		ifValue := rep["if_value"]
		if match == "" {
			continue
		}
		if ifValue != "" && !strings.Contains(content, ifValue) {
			continue
		}
		content = strings.ReplaceAll(content, match, replaceWith)
	}

	return os.WriteFile(target, []byte(content), 0o644)
}

// applyYAML sets dot-notation keys in a YAML document (structure-preserving
// best effort via yaml.Node).
func applyYAML(content string, find map[string]interface{}) (string, error) {
	if len(find) == 0 {
		return content, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return "", fmt.Errorf("yaml parse: %w", err)
	}
	if len(root.Content) == 0 {
		return content, nil
	}
	doc := root.Content[0]
	for _, key := range sortedKeys(find) {
		setYAMLPath(doc, strings.Split(key, "."), find[key])
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// setYAMLPath walks/creates a mapping chain and sets the leaf value.
func setYAMLPath(node *yaml.Node, path []string, value interface{}) {
	if node == nil || node.Kind != yaml.MappingNode || len(path) == 0 {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Value == path[0] {
			if len(path) == 1 {
				encodeYAMLValue(v, value)
			} else if v.Kind == yaml.MappingNode {
				setYAMLPath(v, path[1:], value)
			}
			return
		}
	}
	// key missing: append chain
	key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: path[0]}
	var val *yaml.Node
	if len(path) == 1 {
		val = valueToNode(value)
	} else {
		val = &yaml.Node{Kind: yaml.MappingNode}
		setYAMLPath(val, path[1:], value)
	}
	node.Content = append(node.Content, key, val)
}

func encodeYAMLValue(node *yaml.Node, value interface{}) {
	n := valueToNode(value)
	*node = *n
}

func valueToNode(value interface{}) *yaml.Node {
	b, _ := yaml.Marshal(value)
	var doc yaml.Node
	if yaml.Unmarshal(b, &doc) == nil && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", value)}
}

// applyKeyValue handles properties + ini (key=value lines, section-aware for ini).
func applyKeyValue(content string, find map[string]interface{}, isIni bool) (string, error) {
	if len(find) == 0 {
		return content, nil
	}
	lines := strings.Split(content, "\n")
	section := ""
	found := map[string]bool{}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isIni && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.Trim(trimmed, "[]")
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		eq := strings.IndexAny(trimmed, "=:")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:eq])
		lookup := key
		if isIni && section != "" {
			lookup = section + "." + key
		}
		if v, ok := find[lookup]; ok {
			sep := string(trimmed[eq])
			lines[i] = strings.TrimRight(line[:eq], " \t") + sep + " " + literal(v)
			found[lookup] = true
		}
	}

	// append missing keys
	missing := []string{}
	for k := range find {
		if !found[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		last := len(lines) - 1
		if strings.TrimSpace(lines[last]) != "" {
			lines = append(lines, "")
		}
		for _, k := range missing {
			lines = append(lines, fmt.Sprintf("%s = %s", k, literal(find[k])))
		}
	}

	return strings.Join(lines, "\n"), nil
}

// applyJSON sets dot-notation keys in a JSON document.
func applyJSON(content string, find map[string]interface{}) (string, error) {
	if len(find) == 0 {
		return content, nil
	}
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		return "", fmt.Errorf("json parse: %w", err)
	}
	for _, key := range sortedKeys(find) {
		parts := strings.Split(key, ".")
		cur := doc
		for i, p := range parts {
			if i == len(parts)-1 {
				cur[p] = find[key]
				break
			}
			next, ok := cur[p].(map[string]interface{})
			if !ok {
				next = map[string]interface{}{}
				cur[p] = next
			}
			cur = next
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
