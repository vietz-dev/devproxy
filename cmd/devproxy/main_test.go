package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dreifach-ai/devproxy/internal/devproxy"
	"github.com/spf13/afero"
)

func TestMain(m *testing.M) {
	if os.Getenv("DEVPROXY_SIGNAL_RELAY") == "1" {
		signalRelay()
		return
	}
	os.Exit(m.Run())
}

func executeCommand(args []string, out io.Writer) error {
	root := newRootCommandWithFS(afero.NewMemMapFs())
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(io.Discard)
	return root.Execute()
}

func run(args []string) error {
	return executeCommand(append([]string{"run"}, args...), io.Discard)
}

func register(args []string) error {
	return executeCommand(append([]string{"register"}, args...), io.Discard)
}

func listTo(args []string, out io.Writer) error {
	return executeCommand(append([]string{"list"}, args...), out)
}

func inspectTo(args []string, out io.Writer) error {
	return executeCommand(append([]string{"inspect"}, args...), out)
}

func TestSignalRelayReportsInterrupt(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	relay := exec.Command(os.Args[0])
	relay.Env = append(os.Environ(), "DEVPROXY_SIGNAL_RELAY=1")
	relay.ExtraFiles = []*os.File{writer}
	relay.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := relay.Start(); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	_ = writer.Close()
	defer func() {
		_ = relay.Process.Kill()
		_ = relay.Wait()
	}()
	bytes := make(chan byte, 2)
	go func() {
		var b [1]byte
		for {
			if _, err := reader.Read(b[:]); err != nil {
				return
			}
			bytes <- b[0]
		}
	}()
	for _, want := range []byte{'R', 'I'} {
		if want == 'I' {
			if err := relay.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case got := <-bytes:
			if got != want {
				t.Fatalf("relay sent %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("relay did not send %q", want)
		}
	}
}

func TestHelperHTTPService(t *testing.T) {
	if os.Getenv("DEVPROXY_HELPER") != "1" {
		return
	}
	if os.Getenv("DEVPROXY_URL") != "https://example-test.localhost" {
		os.Exit(2)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
	if err != nil {
		os.Exit(3)
	}
	time.Sleep(1200 * time.Millisecond)
	_ = listener.Close()
	os.Exit(0)
}

func TestHelperHoldPort(t *testing.T) {
	if os.Getenv("DEVPROXY_HOLD_PORT") != "1" {
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
	if err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(os.Getenv("DEVPROXY_HOLD_INFO"), []byte(fmt.Sprintf("%d:%s", os.Getpid(), os.Getenv("PORT"))), 0600); err != nil {
		os.Exit(3)
	}
	defer listener.Close()
	for {
		time.Sleep(time.Hour)
	}
}

func TestHelperInteractiveService(t *testing.T) {
	if os.Getenv("DEVPROXY_INTERACTIVE_CHILD") != "1" {
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
	if err != nil {
		os.Exit(2)
	}
	defer listener.Close()
	if os.Getenv("DEVPROXY_IGNORE_INT") == "1" {
		signal.Ignore(os.Interrupt)
	}
	fmt.Println("INTERACTIVE_READY")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil || line != "ping\n" {
		os.Exit(3)
	}
	fmt.Println("INTERACTIVE_INPUT_OK")
}

func TestRunPreservesInteractiveInput(t *testing.T) {
	if os.Getenv("DEVPROXY_INTERACTIVE_TEST") != "1" {
		t.Skip("run the compiled test binary with DEVPROXY_INTERACTIVE_TEST=1 in a terminal")
	}
	if group, ok := terminalForegroundGroup(os.Stdin.Fd()); !ok || (int(group) != syscall.Getpgrp() && os.Getenv("DEVPROXY_BACKGROUND_TEST") != "1") {
		t.Skip("interactive test needs a foreground terminal")
	}
	t.Setenv("DEVPROXY_INTERACTIVE_CHILD", "1")
	release := make(chan struct{})
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/register" {
			if os.Getenv("DEVPROXY_STALL_REGISTER") == "1" {
				select {
				case <-r.Context().Done():
				case <-release:
				}
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer daemon.Close()
	defer close(release)
	if err := run([]string{"--project", "example", "--worktree", "test", "--daemon", daemon.URL,
		"web", "--", os.Args[0], "-test.run=TestHelperInteractiveService"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithRedirectedStdin(t *testing.T) {
	if os.Getenv("DEVPROXY_REDIRECTED_TEST") != "1" {
		t.Skip("run the compiled test binary with redirected stdin in a terminal")
	}
	t.Setenv("DEVPROXY_HOLD_PORT", "1")
	t.Setenv("DEVPROXY_HOLD_INFO", t.TempDir()+"/child-info")
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/register" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer daemon.Close()
	if err := run([]string{"--project", "example", "--worktree", "redirected", "--daemon", daemon.URL,
		"web", "--", os.Args[0], "-test.run=TestHelperHoldPort"}); err != nil {
		t.Fatal(err)
	}
}

func TestRunStopsServerBehindShellWrapper(t *testing.T) {
	info := t.TempDir() + "/child-info"
	t.Setenv("DEVPROXY_HOLD_PORT", "1")
	t.Setenv("DEVPROXY_HOLD_INFO", info)
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "registration rejected", http.StatusBadGateway)
	}))
	defer daemon.Close()
	err := run([]string{"--project", "example", "--worktree", "test", "--daemon", daemon.URL,
		"--ready-timeout", "3s", "web", "--", "sh", "-c", `"$1" -test.run=TestHelperHoldPort & wait`, "sh", os.Args[0]})
	if err == nil {
		t.Fatal("expected registration to fail")
	}
	data, err := os.ReadFile(info)
	if err != nil {
		t.Fatalf("server helper did not start: %v", err)
	}
	parts := strings.SplitN(string(data), ":", 2)
	if len(parts) != 2 {
		t.Fatalf("invalid helper info: %q", data)
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	address := "127.0.0.1:" + parts[1]
	t.Cleanup(func() {
		if conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
			_ = conn.Close()
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server behind shell wrapper still listens on %s after devproxy exits", address)
}

func TestKeepAliveHandlesSignalDuringStalledHeartbeat(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/heartbeat" {
			close(started)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer daemon.Close()
	defer close(release)
	ticks := make(chan time.Time, 1)
	signals := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- keepAliveWithTicks(daemon.URL, devproxy.Registration{ID: strings.Repeat("a", 32)}, nil, signals, nil, ticks)
	}()
	ticks <- time.Now()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("heartbeat never started")
	}
	signals <- syscall.SIGTERM
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("signal handling waited for a stalled heartbeat")
	}
}

func TestRunRegistersAndUnregistersService(t *testing.T) {
	var mu sync.Mutex
	var sawRoute bool
	caddy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "example-test.localhost") {
			mu.Lock()
			sawRoute = true
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer caddy.Close()
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	listen := "127.0.0.1:" + strconv.Itoa(port)
	server, err := devproxy.NewServer(caddy.URL, listen, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	client := &http.Client{Timeout: time.Second}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		response, err := client.Get("http://" + listen + "/v1/services")
		if err == nil {
			_ = response.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Setenv("DEVPROXY_HELPER", "1")
	if err := run([]string{"web", "--project", "example", "--worktree", "test", "--daemon", "http://" + listen, "--", os.Args[0], "-test.run=TestHelperHTTPService"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	seen := sawRoute
	mu.Unlock()
	if !seen {
		t.Fatal("Caddy never received the worktree route")
	}
	response, err := client.Get("http://" + listen + "/v1/services")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if strings.Contains(string(body), "example-test.localhost") {
		t.Fatalf("service remained registered after child exit: %s", body)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestListAndInspectOutput(t *testing.T) {
	t.Setenv("DEVPROXY_JSON", "")
	t.Setenv("DEVPROXY_ROUTE", "")
	services := []devproxy.Service{
		{Registration: devproxy.Registration{ID: strings.Repeat("a", 32), Project: "example", Worktree: "feature", Service: "web", Route: "/", Port: 3000}, Host: "example-feature.localhost", URL: "https://example-feature.localhost", ExpiresAt: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)},
		{Registration: devproxy.Registration{ID: strings.Repeat("b", 32), Project: "example", Worktree: "feature", Service: "api", Route: "/api", Port: 3001}, Host: "example-feature.localhost", URL: "https://example-feature.localhost", ExpiresAt: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)},
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/services" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(services)
	}))
	defer daemon.Close()

	var out bytes.Buffer
	if err := listTo([]string{"--daemon", daemon.URL}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "https://example-feature.localhost") || !strings.Contains(out.String(), "127.0.0.1:3001") {
		t.Fatalf("human list missing service details: %q", out.String())
	}
	out.Reset()
	if err := listTo([]string{"--daemon", daemon.URL, "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var listed []devproxy.Service
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("list JSON: %v; output %q", err, out.String())
	}
	if len(listed) != 2 || listed[0].ID != services[0].ID || listed[1].Route != "/api" {
		t.Fatalf("unexpected list JSON: %+v", listed)
	}
	out.Reset()
	if err := executeCommand([]string{"services", "list", "--daemon", daemon.URL, "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil || len(listed) != 2 {
		t.Fatalf("services list JSON: %v; output %q", err, out.String())
	}

	for _, target := range []string{services[1].ID, services[1].Host, services[1].URL} {
		out.Reset()
		args := []string{"--daemon", daemon.URL, "--json"}
		if target != services[1].ID {
			args = append(args, "--route", "/api")
		}
		args = append([]string{target}, args...)
		if err := inspectTo(args, &out); err != nil {
			t.Fatalf("inspect %s: %v", target, err)
		}
		var got devproxy.Service
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("inspect JSON: %v", err)
		}
		if got.ID != services[1].ID || got.Port != 3001 {
			t.Fatalf("inspect %s returned %+v", target, got)
		}
	}
	out.Reset()
	if err := inspectTo([]string{"--daemon", daemon.URL, services[0].ID}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Service:  web") || !strings.Contains(out.String(), "Expires:  2026-10-17T12:00:00Z") {
		t.Fatalf("human inspect missing details: %q", out.String())
	}
	if err := inspectTo([]string{"--daemon", daemon.URL, services[0].Host}, &out); err == nil || !strings.Contains(err.Error(), "matches 2 registrations") {
		t.Fatalf("expected ambiguous host error, got %v", err)
	}
	if err := inspectTo([]string{"--daemon", daemon.URL, "missing.localhost"}, &out); err == nil || !strings.Contains(err.Error(), "no active registration") {
		t.Fatalf("expected missing registration error, got %v", err)
	}
}

func TestRegisterAcceptsFlagsAfterService(t *testing.T) {
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	err = register([]string{"web", "--project", "example", "--worktree", "test", "--port", strconv.Itoa(port)})
	if err == nil || !strings.Contains(err.Error(), "service is not listening") {
		t.Fatalf("expected connection check after parsing flags, got %v", err)
	}
}

func TestRunHelpWithoutCommand(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}} {
		var out bytes.Buffer
		if err := executeCommand(append([]string{"run"}, args...), &out); err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
		if !strings.Contains(out.String(), "Usage:\n  devproxy run") || !strings.Contains(out.String(), "--ready-timeout") {
			t.Fatalf("run %v: incomplete help output: %q", args, out.String())
		}
	}
}

func TestListJSONEmptyArray(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("null"))
	}))
	defer daemon.Close()
	var out bytes.Buffer
	if err := listTo([]string{"--daemon", daemon.URL, "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "[]\n" {
		t.Fatalf("empty list JSON = %q, want []", out.String())
	}
}

func TestConfigPrecedenceForDaemon(t *testing.T) {
	fs := afero.NewMemMapFs()
	t.Setenv("XDG_CONFIG_HOME", "/config")
	t.Setenv("DEVPROXY_DAEMON", "")
	if err := fs.MkdirAll("/config/devproxy", 0700); err != nil {
		t.Fatal(err)
	}
	servers := make([]*httptest.Server, 4)
	for i := range servers {
		name := fmt.Sprintf("source-%d", i)
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode([]devproxy.Service{{Registration: devproxy.Registration{Service: name}}})
		}))
		defer servers[i].Close()
	}
	for path, value := range map[string]string{
		"/config/devproxy/config.yaml": "daemon: " + servers[0].URL + "\n",
		".devproxy.yaml":               "daemon: " + servers[1].URL + "\n",
	} {
		if err := afero.WriteFile(fs, path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(args []string, want string) {
		t.Helper()
		var out bytes.Buffer
		cmd := newRootCommandWithFS(fs)
		cmd.SetArgs(append([]string{"list", "--json"}, args...))
		cmd.SetOut(&out)
		cmd.SetErr(io.Discard)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), want) {
			t.Fatalf("list output %q does not contain %q", out.String(), want)
		}
	}
	check(nil, "source-1")
	t.Setenv("DEVPROXY_DAEMON", servers[2].URL)
	check(nil, "source-2")
	check([]string{"--daemon", servers[3].URL}, "source-3")
}

func TestUsageAndLookupErrorsHaveSemanticExitCodes(t *testing.T) {
	for _, args := range [][]string{{"run"}, {"register", "web"}, {"list", "--unknown"}} {
		var out bytes.Buffer
		err := executeCommand(args, &out)
		var exit interface{ ExitCode() int }
		if err == nil || !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Fatalf("%v: expected exit 2, got %v", args, err)
		}
	}
	var out bytes.Buffer
	renderCLIError(&out, notFound("missing", "devproxy list"), true)
	if !strings.Contains(out.String(), `"exit_code":4`) || !strings.Contains(out.String(), `"run":"devproxy list"`) {
		t.Fatalf("JSON error envelope: %q", out.String())
	}
}

func TestInvalidCaddyAdminURLIsUsageError(t *testing.T) {
	err := serve("127.0.0.1:7654", "https://127.0.0.1:2019", false)
	var exit interface{ ExitCode() int }
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("invalid Caddy admin URL: expected exit 2, got %v", err)
	}
}

func TestQuietServiceFlagsAndConfiguredJSONErrors(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, ".devproxy.yaml", []byte("json: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root, config := newRootCommandWithConfig(fs)
	root.SetArgs([]string{"serve", "--listen", "0.0.0.0:7654"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil || !errorAsJSON(config, []string{"serve"}) {
		t.Fatalf("configured JSON error mode: err=%v loaded=%v", err, config.loaded)
	}
	if wantsJSON([]string{"--json=false", "--json"}) != true || wantsJSON([]string{"--json", "--json=false"}) != false {
		t.Fatal("last --json flag should determine error mode")
	}
	config.v.Set("quiet", true)
	f := serviceFlags{project: new(string), worktree: new(string), route: new(string), daemon: new(string)}
	f.apply(config)
	if !f.quiet {
		t.Fatal("--quiet did not reach the service workflow")
	}
}

func TestBooleanEnvironmentAndConfigLoadErrorFormatting(t *testing.T) {
	t.Setenv("DEVPROXY_JSON", "true")
	t.Setenv("DEVPROXY_QUIET", "true")
	if !errorAsJSON(newCLIConfig(afero.NewMemMapFs()), []string{"list", "--unknown"}) {
		t.Fatal("DEVPROXY_JSON must format errors before command pre-run")
	}
	fs := afero.NewMemMapFs()
	root, config := newRootCommandWithConfig(fs)
	var out bytes.Buffer
	root.SetArgs([]string{"config", "get", "json", "--origin"})
	root.SetOut(&out)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Value  bool   `json:"value"`
		Origin string `json:"origin"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Value || result.Origin != "environment" || !config.getBool("quiet") {
		t.Fatalf("boolean environment settings: %+v quiet=%v", result, config.getBool("quiet"))
	}

	t.Setenv("DEVPROXY_JSON", "")
	t.Setenv("XDG_CONFIG_HOME", "/config")
	if err := fs.MkdirAll("/config/devproxy", 0700); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "/config/devproxy/config.yaml", []byte("json: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root, config = newRootCommandWithConfig(fs)
	root.SetArgs([]string{"list", "--config", "/missing.yaml"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	if err := root.Execute(); err == nil || !errorAsJSON(config, []string{"list", "--config", "/missing.yaml"}) {
		t.Fatalf("expected JSON mode after config load failure, got %v", err)
	}
	if errorAsJSON(config, []string{"list", "--config", "/missing.yaml", "--json=false"}) {
		t.Fatal("explicit --json=false should override partial config")
	}
}

func TestPlainListEscapesRecordDelimiters(t *testing.T) {
	service := devproxy.Service{
		Registration: devproxy.Registration{Service: "web\tapi\nworker\rtest", Route: "/", Port: 3000},
		URL:          "https://example.localhost",
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]devproxy.Service{service})
	}))
	defer daemon.Close()
	var out bytes.Buffer
	if err := listServicesFormatted(daemon.URL, false, true, &out); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n"); len(lines) != 1 {
		t.Fatalf("plain output added records: %q", out.String())
	}
	fields := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\t")
	if len(fields) != 4 || fields[2] != `web\tapi\nworker\rtest` {
		t.Fatalf("plain output fields: %q", out.String())
	}
}

func TestFileConfiguredJSONOnEarlyFlagError(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, ".devproxy.yaml", []byte("json: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root, config := newRootCommandWithConfig(fs)
	args := []string{"services", "describe", "--unknown"}
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	var typed *cliError
	if !errors.As(err, &typed) || typed.run != "devproxy services describe --help" {
		t.Fatalf("nested flag recovery command: %v", err)
	}
	if !errorAsJSONAfterExecution(root, config, args) {
		t.Fatal("file-configured JSON must format early flag errors")
	}
}

func TestExplicitJSONOnEarlyFlagError(t *testing.T) {
	for _, args := range [][]string{
		{"list", "--json", "--unknown"},
		{"list", "--unknown", "--json"},
	} {
		root, config := newRootCommandWithConfig(afero.NewMemMapFs())
		root.SetArgs(args)
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		if err := root.Execute(); err == nil {
			t.Fatalf("%v: expected flag error", args)
		}
		if !errorAsJSONAfterExecution(root, config, args) {
			t.Fatalf("%v: explicit --json was ignored", args)
		}
	}
	for _, flag := range []string{"--json=false", "--json=FALSE", "--json=0"} {
		root, config := newRootCommandWithConfig(afero.NewMemMapFs())
		args := []string{"list", "--unknown", flag}
		root.SetArgs(args)
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		if err := root.Execute(); err == nil {
			t.Fatalf("%v: expected flag error", args)
		}
		if errorAsJSONAfterExecution(root, config, args) {
			t.Fatalf("%v: false JSON flag was ignored", args)
		}
	}
}

func TestExplicitConfigAfterEarlyFlagError(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "custom.yaml", []byte("json: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"list", "--unknown", "--config", "custom.yaml"},
		{"list", "--unknown", "--config=custom.yaml"},
	} {
		root, config := newRootCommandWithConfig(fs)
		root.SetArgs(args)
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		if err := root.Execute(); err == nil {
			t.Fatalf("%v: expected flag error", args)
		}
		if !errorAsJSONAfterExecution(root, config, args) {
			t.Fatalf("%v: explicit config JSON was ignored", args)
		}
	}
}
