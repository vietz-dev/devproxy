package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

func TestProjectConfigValidation(t *testing.T) {
	tests := []struct{ name, yaml, want string }{
		{"missing version", "project: example\n", "add version: 1"},
		{"empty", "", "version: 1"},
		{"unknown", "version: 1\nporrt: 3000\n", `unknown key "porrt"`},
		{"case", "version: 1\nProject: example\n", `unknown key "Project"`},
		{"future", "version: 2\n", "supports version: 1"},
		{"version string", "version: '1'\n", "version must be an integer"},
		{"version bool", "version: true\n", "version must be an integer"},
		{"string", "version: 1\nproject: 123\n", "project must be a string"},
		{"bool", "version: 1\njson: 'true'\n", "json must be a boolean"},
		{"integer", "version: 1\nport: 1.5\n", "port must be an integer"},
		{"overflow", "version: 1\nport: 9999999999999999999999999999999\n", "port must be"},
		{"null", "version: 1\nroute: null\n", "route must be a string"},
		{"nested", "version: 1\nproject: {name: example}\n", "project must be a string"},
		{"duration", "version: 1\nready-timeout: soon\n", "ready-timeout must be a duration"},
		{"duplicate", "version: 1\nproject: a\nproject: b\n", "duplicate key"},
		{"alias", "version: 1\nproject: &name example\nworktree: *name\n", "worktree must be a string"},
		{"merge", "version: 1\n<<: {project: example}\n", "setting names must be strings"},
		{"documents", "version: 1\n---\nproject: other\n", "exactly one YAML document"},
		{"sequence", "- project\n", "expected a YAML settings mapping"},
		{"malformed", "version: [\n", "expected a YAML settings mapping"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			if err := afero.WriteFile(fs, ".devproxy.yaml", []byte(tt.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			// Validation must run before the child, regardless of overrides.
			t.Setenv("DEVPROXY_PROJECT", "override")
			root := newRootCommandWithFS(fs)
			root.SetArgs([]string{"run", "--project", "override", "web", "--", "not-a-real-command"})
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), ".devproxy.yaml") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected file and %q, got %v", tt.want, err)
			}
		})
	}
}

func TestConfigSchemaCompatibility(t *testing.T) {
	for _, tt := range []struct {
		name, yaml     string
		project, valid bool
	}{
		{"minimal", "version: 1\n", true, true},
		{"settings", "version: 1\nproject: example\nroute: /\nport: 3000\njson: false\nready-timeout: 30s\n", true, true},
		{"legacy", "project: 123\ncustom: value\n", false, true},
		{"legacy empty", "", false, true},
		{"opt in", "version: 1\ncustom: value\n", false, false},
		{"unsupported opt in", "version: 2\n", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig([]byte(tt.yaml), tt.project)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, got %v", tt.valid, err)
			}
		})
	}
}

func TestVersionedConfigPrecedenceAndOrigins(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/config")
	t.Setenv("DEVPROXY_PROJECT", "")
	fs := afero.NewMemMapFs()
	if err := fs.MkdirAll("/config/devproxy", 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		"/config/devproxy/config.yaml": "project: user\n",
		".devproxy.yaml":               "version: 1\nproject: project\n",
		"custom.yaml":                  "project: explicit\n",
	} {
		if err := afero.WriteFile(fs, path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(args []string, value, origin string) {
		t.Helper()
		root, config := newRootCommandWithConfig(fs)
		root.SetArgs(append([]string{"config", "get", "project"}, args...))
		var out bytes.Buffer
		root.SetOut(&out)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if config.getString("project") != value || config.origin["project"] != origin {
			t.Fatalf("value=%q origin=%q", config.getString("project"), config.origin["project"])
		}
	}
	check(nil, "project", ".devproxy.yaml")
	check([]string{"--config", "custom.yaml"}, "explicit", "custom.yaml")
	t.Setenv("DEVPROXY_PROJECT", "environment")
	check([]string{"--config", "custom.yaml"}, "environment", "custom.yaml")
}
