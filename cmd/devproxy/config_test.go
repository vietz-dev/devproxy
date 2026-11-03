package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

func configTestEnvironment(t *testing.T) {
	t.Helper()
	for key := range configDefaults {
		t.Setenv("DEVPROXY_"+strings.ToUpper(strings.ReplaceAll(key, "-", "_")), "")
	}
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_CEILING_DIRECTORIES"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func configTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeConfigTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func configTestGet(t *testing.T, key string, args ...string) (string, error) {
	t.Helper()
	root := newRootCommandWithFS(afero.NewOsFs())
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{"config", "get", key, "--origin"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestProjectConfigWorktreeDiscovery(t *testing.T) {
	configTestEnvironment(t)
	repo := t.TempDir()
	configTestGit(t, repo, "init")
	configTestGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "initial")
	linked := filepath.Join(t.TempDir(), "linked")
	configTestGit(t, repo, "worktree", "add", "--detach", linked)
	for _, root := range []string{repo, linked} {
		// Use Git's canonical path for --origin (macOS temp paths use symlinks).
		root = configTestGit(t, root, "rev-parse", "--show-toplevel")
		projectFile := filepath.Join(root, ".devproxy.yaml")
		writeConfigTestFile(t, projectFile, fmt.Sprintf("version: 1\nproject: %q\n", filepath.Base(root)))
		nested := filepath.Join(root, "packages", "web")
		writeConfigTestFile(t, filepath.Join(nested, ".devproxy.yaml"), "project: ignored\n")
		for _, dir := range []string{root, nested} {
			t.Run(dir, func(t *testing.T) {
				t.Chdir(dir)
				got, err := configTestGet(t, "project")
				want := fmt.Sprintf("%s\t%s\n", filepath.Base(root), projectFile)
				if err != nil || got != want {
					t.Fatalf("config get = %q, %v; want %q", got, err, want)
				}
			})
		}
		if err := os.Remove(projectFile); err != nil {
			t.Fatal(err)
		}
		t.Run(root+"/missing-root-config", func(t *testing.T) {
			t.Chdir(nested)
			got, err := configTestGet(t, "project")
			if err != nil || got != "\tdefault\n" {
				t.Fatalf("nested config must not replace missing root config: %q, %v", got, err)
			}
		})
	}
}

func TestProjectConfigOutsideGit(t *testing.T) {
	configTestEnvironment(t)
	parent := t.TempDir()
	writeConfigTestFile(t, filepath.Join(parent, ".devproxy.yaml"), "project: parent\n")
	dir := filepath.Join(parent, "nested")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	got, err := configTestGet(t, "project")
	if err != nil || got != "\tdefault\n" {
		t.Fatalf("outside Git must not search ancestors: %q, %v", got, err)
	}
	writeConfigTestFile(t, ".devproxy.yaml", "version: 1\nproject: local\n")
	for _, noGit := range []bool{false, true} {
		t.Run(fmt.Sprintf("git-unavailable=%t", noGit), func(t *testing.T) {
			if noGit {
				t.Setenv("PATH", t.TempDir())
			}
			got, err := configTestGet(t, "project")
			if err != nil || got != "local\t.devproxy.yaml\n" {
				t.Fatalf("current directory fallback: %q, %v", got, err)
			}
		})
	}
}

func TestWorktreeConfigPrecedenceAndExplicitPath(t *testing.T) {
	configTestEnvironment(t)
	repo := t.TempDir()
	configTestGit(t, repo, "init")
	repo = configTestGit(t, repo, "rev-parse", "--show-toplevel")
	nested := filepath.Join(repo, "packages", "web")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	check := func(want string, args ...string) {
		t.Helper()
		got, err := configTestGet(t, "daemon", args...)
		if err != nil || got != want {
			t.Fatalf("config get %v = %q, %v; want %q", args, got, err, want)
		}
	}
	check(defaultDaemon + "\tdefault\n")
	user := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "devproxy", "config.yaml")
	writeConfigTestFile(t, user, "daemon: http://user.localhost\n")
	check("http://user.localhost\t" + user + "\n")
	project := filepath.Join(repo, ".devproxy.yaml")
	writeConfigTestFile(t, project, "version: 1\ndaemon: http://project.localhost\nproject: configured\nworktree: configured\n")
	check("http://project.localhost\t" + project + "\n")
	writeConfigTestFile(t, filepath.Join(repo, "custom.yaml"), "daemon: http://wrong.localhost\n")
	writeConfigTestFile(t, "custom.yaml", "daemon: http://explicit.localhost\n")
	check("http://explicit.localhost\tcustom.yaml\n", "--config", "custom.yaml")
	t.Setenv("DEVPROXY_DAEMON", "http://environment.localhost")
	check("http://environment.localhost\tenvironment\n", "--config", "custom.yaml")
	check("http://flag.localhost\tflag\n", "--config", "custom.yaml", "--daemon", "http://flag.localhost")
	if _, err := configTestGet(t, "daemon", "--config", "missing.yaml"); err == nil || !strings.Contains(err.Error(), "read config missing.yaml") {
		t.Fatalf("missing explicit config must fail: %v", err)
	}
	// Service flags must still override the discovered project/worktree settings.
	root, config := newRootCommandWithConfig(afero.NewOsFs())
	run, _, err := root.Find([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}
	run.RunE = func(_ *cobra.Command, _ []string) error {
		for _, key := range []string{"project", "worktree"} {
			if got := config.getString(key); got != "override" {
				t.Errorf("--%s lost precedence: %q", key, got)
			}
		}
		return nil
	}
	root.SetArgs([]string{"run", "--project", "override", "--worktree", "override", "web", "--", "echo"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}
