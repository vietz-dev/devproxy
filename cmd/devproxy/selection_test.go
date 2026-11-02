package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/vietz-dev/devproxy/internal/devproxy"
)

func TestSelectionDiagnostics(t *testing.T) {
	t.Setenv("DEVPROXY_ROUTE", "")
	t.Setenv("DEVPROXY_JSON", "")
	const host = "example-feature.localhost"
	const origin = "https://" + host
	const firstID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const secondID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	// Reverse ID order and avoid /api to catch hard-coded corrective selectors.
	services := []devproxy.Service{
		{Registration: devproxy.Registration{ID: secondID, Route: "/assets"}, Host: host, URL: origin},
		{Registration: devproxy.Registration{ID: firstID, Route: "/"}, Host: host, URL: origin},
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/services" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(services)
	}))
	defer daemon.Close()

	for _, command := range [][]string{{"inspect"}, {"services", "describe"}, {"services", "inspect"}} {
		for _, tc := range []struct {
			name, target, route string
			code                int
			available           bool
		}{
			{name: "ambiguous host", target: host, code: 5},
			{name: "ambiguous URL", target: origin, code: 5},
			{name: "ID", target: secondID},
			{name: "host route", target: host, route: "/assets"},
			{name: "URL route", target: origin, route: "/assets"},
			{name: "ID route", target: secondID, route: "/assets"},
			{name: "missing ID", target: strings.Repeat("c", 32), code: 4},
			{name: "missing host", target: "missing.localhost", code: 4},
			{name: "missing URL", target: "https://missing.localhost", code: 4},
			{name: "URL path", target: origin + "/assets", code: 4},
			{name: "missing host route", target: host, route: "/missing", code: 4, available: true},
			{name: "missing URL route", target: origin, route: "/missing", code: 4, available: true},
			{name: "missing ID route", target: secondID, route: "/missing", code: 4, available: true},
		} {
			t.Run(strings.Join(command, " ")+"/"+tc.name, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				root := newRootCommandWithFS(afero.NewMemMapFs())
				args := append(append([]string{}, command...), "--daemon", daemon.URL, "--json")
				if tc.route != "" {
					args = append(args, "--route", tc.route)
				}
				root.SetArgs(append(args, tc.target))
				root.SetOut(&stdout)
				root.SetErr(&stderr)
				err := root.Execute()
				if tc.code == 0 {
					if err != nil {
						t.Fatal(err)
					}
					var got devproxy.Service
					if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.ID != secondID || stderr.Len() != 0 {
						t.Fatalf("wrong selection: stdout=%s stderr=%s err=%v", &stdout, &stderr, err)
					}
					return
				}
				var typed *cliError
				if !errors.As(err, &typed) || typed.ExitCode() != tc.code {
					t.Fatalf("want exit %d, got %v", tc.code, err)
				}
				renderCLIError(&stderr, err, true)
				var envelope map[string]map[string]any
				if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
					t.Fatalf("invalid JSON error: %s: %v", &stderr, err)
				}
				code := "NOT_FOUND"
				if tc.code == 5 {
					code = "CONFLICT"
				}
				want := map[string]map[string]any{"error": {
					"code": code, "exit_code": float64(tc.code), "message": typed.message, "run": typed.run, "retryable": false,
				}}
				if !reflect.DeepEqual(envelope, want) || stdout.Len() != 0 {
					t.Fatalf("error contract changed: stdout=%s stderr=%s", &stdout, &stderr)
				}
				if tc.code == 5 || tc.available {
					if !strings.Contains(typed.message, secondID) || !strings.Contains(typed.message, `route="/assets"`) || !strings.Contains(typed.message, host) {
						t.Fatalf("missing competing identity: %s", typed.message)
					}
				}
				if tc.code == 5 {
					if strings.Index(typed.message, firstID) > strings.Index(typed.message, secondID) || !strings.Contains(typed.message, "--route") {
						t.Fatalf("unsorted or unhelpful ambiguity: %s", typed.message)
					}
					if typed.run != "devproxy inspect --daemon "+daemon.URL+" --route / -- "+firstID {
						t.Fatalf("incorrect corrective command: %s", typed.run)
					}
				} else {
					for _, form := range []string{"exact registration ID", "hostname", "origin URL", "--route"} {
						if !strings.Contains(typed.message, form) {
							t.Fatalf("missing lookup guidance %q: %s", form, typed.message)
						}
					}
					if tc.route != "" && !strings.Contains(typed.message, `with route "/missing"`) {
						t.Fatalf("missing failing route: %s", typed.message)
					}
					if typed.run != "devproxy list --daemon "+daemon.URL+" --json" {
						t.Fatalf("incorrect discovery command: %s", typed.run)
					}
				}
				stderr.Reset()
				renderCLIError(&stderr, err, false)
				if !strings.Contains(stderr.String(), typed.message) || !strings.Contains(stderr.String(), "Run: "+typed.run) {
					t.Fatalf("human error missing context: %s", &stderr)
				}
			})
		}
	}
}

func TestSelectionCommandQuotesShellArguments(t *testing.T) {
	for _, value := range []string{"normal-id", "", "a'b", "$(echo unsafe)", "#comment", "~", "a\nb"} {
		out, err := exec.Command("sh", "-c", "printf '%s' "+shellArgument(value)).Output()
		if err != nil || string(out) != value {
			t.Fatalf("shell argument %q: got %q, err=%v", value, out, err)
		}
	}
}

func TestSelectionCorrectionOverridesConfiguration(t *testing.T) {
	t.Setenv("DEVPROXY_DAEMON", "http://configured.invalid")
	t.Setenv("DEVPROXY_ROUTE", "/")
	t.Setenv("DEVPROXY_JSON", "false")
	services := []devproxy.Service{
		{Registration: devproxy.Registration{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Route: "/assets"}, Host: "example.localhost"},
		{Registration: devproxy.Registration{ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Route: "/"}, Host: "example.localhost"},
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(services)
	}))
	defer daemon.Close()
	var out bytes.Buffer
	root := newRootCommandWithFS(afero.NewMemMapFs())
	root.SetOut(&out)
	root.SetArgs([]string{"inspect", "--daemon", daemon.URL, "--route=", "example.localhost"})
	var typed *cliError
	if err := root.Execute(); !errors.As(err, &typed) || typed.code != 5 {
		t.Fatalf("expected ambiguous host: %v", err)
	}
	correction := newRootCommandWithFS(afero.NewMemMapFs())
	correction.SetOut(&out)
	correction.SetArgs(append([]string{"--json"}, strings.Fields(typed.run)[1:]...))
	if err := correction.Execute(); err != nil {
		t.Fatalf("corrective command failed: %s: %v", typed.run, err)
	}
	var got devproxy.Service
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.ID != services[0].ID {
		t.Fatalf("correction selected wrong service: %s: %v", &out, err)
	}
	// Even the built-in daemon must be pinned: omitting it would fall back to
	// DEVPROXY_DAEMON or a config file on the next invocation.
	for _, command := range []string{"inspect", "list"} {
		if got := selectionCommand(defaultDaemon, command); got != "devproxy "+command+" --daemon "+defaultDaemon {
			t.Fatalf("default daemon override lost: %s", got)
		}
	}
}
