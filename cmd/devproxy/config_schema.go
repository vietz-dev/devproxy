package main

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Project files must opt into the schema. Legacy user and explicit files keep
// their permissive behavior unless they declare a version themselves.
func validateConfig(data []byte, project bool) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		if err == io.EOF && !project {
			return nil
		}
		return fmt.Errorf("expected a YAML settings mapping with version: 1: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("expected a YAML settings mapping; use version: 1 followed by setting: value entries")
	}
	root := doc.Content[0]
	var version *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "version" {
			version = root.Content[i+1]
		}
	}
	if version == nil && !project {
		return nil
	}
	if version == nil {
		return fmt.Errorf("missing required version; add version: 1 to migrate this project file")
	}
	if version.Kind != yaml.ScalarNode || version.Tag != "!!int" {
		return fmt.Errorf("line %d: version must be an integer; use version: 1", version.Line)
	}
	var v int
	if err := version.Decode(&v); err != nil || v != 1 {
		return fmt.Errorf("line %d: unsupported version %q; this CLI supports version: 1", version.Line, version.Value)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected exactly one YAML document; remove additional documents or fix trailing YAML")
	}
	seen := make(map[string]bool)
	for i := 0; i < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return fmt.Errorf("line %d: setting names must be strings; use flat setting: value entries", key.Line)
		}
		if seen[key.Value] {
			return fmt.Errorf("line %d: duplicate key %q; keep only one value", key.Line, key.Value)
		}
		seen[key.Value] = true
		if key.Value == "version" {
			continue
		}
		def, ok := configDefaults[key.Value]
		if !ok {
			keys := []string{"version"}
			for name := range configDefaults {
				keys = append(keys, name)
			}
			sort.Strings(keys)
			return fmt.Errorf("line %d: unknown key %q; remove it or use one of: %s", key.Line, key.Value, strings.Join(keys, ", "))
		}
		tag, expected := "!!str", "a string"
		switch def.(type) {
		case bool:
			tag, expected = "!!bool", "a boolean (true or false)"
		case int:
			tag, expected = "!!int", "an integer"
		}
		if value.Kind != yaml.ScalarNode || value.Tag != tag {
			return fmt.Errorf("line %d: %s must be %s; correct its YAML value", value.Line, key.Value, expected)
		}
		if tag == "!!int" {
			var n int
			if err := value.Decode(&n); err != nil {
				return fmt.Errorf("line %d: %s must fit a machine integer; reduce its value", value.Line, key.Value)
			}
		}
		if key.Value == "ready-timeout" {
			if _, err := time.ParseDuration(value.Value); err != nil {
				return fmt.Errorf("line %d: ready-timeout must be a duration string; use e.g. 30s or 1m", value.Line)
			}
		}
	}
	return nil
}
