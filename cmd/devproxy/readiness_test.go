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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/vietz-dev/devproxy/internal/devproxy"
)

func TestHelperReadinessCLI(t *testing.T) {
	if os.Getenv("DEVPROXY_READINESS_CLI") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(99)
}

func TestHelperReadinessChild(t *testing.T) {
	if os.Getenv("DEVPROXY_READINESS_CHILD") != "1" {
		return
	}
	fmt.Println("child stdout")
	fmt.Fprintln(os.Stderr, "child stderr")
	listener, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
	if err != nil {
		os.Exit(3)
	}
	defer listener.Close()
	if path := os.Getenv("DEVPROXY_READINESS_CHILD_PID"); path != "" {
		if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(4)
		}
	}
	// Deliberately not an HTTP server: readiness must not imply HTTP health.
	for {
		time.Sleep(time.Hour)
	}
}

func readinessCLI(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestHelperReadinessCLI$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "DEVPROXY_READINESS_CLI=1", "DEVPROXY_READINESS_CHILD=1", "XDG_CONFIG_HOME="+t.TempDir())
	cmd.Dir = t.TempDir()
	return cmd
}

func TestServiceReadinessJSON(t *testing.T) {
	for _, command := range []string{"run", "register", "services run", "services register"} {
		t.Run(command, func(t *testing.T) {
			registrations := make(chan devproxy.Registration, 2)
			revoked := make(chan string, 1)
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/register":
					var registration devproxy.Registration
					_ = json.NewDecoder(r.Body).Decode(&registration)
					registrations <- registration
				case "/v1/unregister":
					var input map[string]string
					_ = json.NewDecoder(r.Body).Decode(&input)
					revoked <- input["id"]
				}
			}))
			defer daemon.Close()
			args := append(strings.Fields(command), "--json", "--quiet", "--project", "example", "--worktree", "test", "--route", "/api", "--daemon", daemon.URL)
			if strings.HasSuffix(command, "register") {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				args = append(args, "--port", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port), "api")
			} else {
				args = append(args, "api", "--", os.Args[0], "-test.run=^TestHelperReadinessChild$")
			}
			cmd := readinessCLI(t, args...)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill() }()
			scanner := bufio.NewScanner(stdout)
			if strings.HasSuffix(command, "run") {
				if !scanner.Scan() || scanner.Text() != "child stdout" {
					t.Fatalf("child stdout changed: %q", scanner.Text())
				}
			}
			if !scanner.Scan() {
				t.Fatal("no readiness record before EOF")
			}
			var result serviceReadyV1
			if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
				t.Fatalf("invalid readiness %q: %v", scanner.Text(), err)
			}
			registration := <-registrations
			if result.SchemaVersion != 1 || result.Event != "service_ready" || result.ID == "" || result.ID != registration.ID ||
				result.Project != "example" || result.Worktree != "test" || result.Service != "api" || result.Route != "/api" ||
				result.URL != "https://example-test.localhost" || result.Port != registration.Port || result.Port < 1 ||
				result.Readiness.Registration != "accepted" || result.Readiness.TCP != "listening" || result.Readiness.HTTPHealth != "not_checked" {
				t.Fatalf("unexpected result: %+v", result)
			}
			// The record must be readable without EOF and must not end supervision.
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			if scanner.Scan() {
				t.Fatalf("unexpected second record: %s", scanner.Text())
			}
			if err := cmd.Wait(); err != nil {
				t.Fatalf("shutdown: %v; stderr=%s", err, &stderr)
			}
			if got := <-revoked; got != result.ID {
				t.Fatalf("revoked %q, want %q", got, result.ID)
			}
			wantStderr := ""
			if strings.HasSuffix(command, "run") {
				wantStderr = "child stderr\n"
			}
			if stderr.String() != wantStderr {
				t.Fatalf("stderr=%q, want %q", stderr.String(), wantStderr)
			}
		})
	}
}

func TestServiceReadinessFailures(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	closedPort, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "registration rejected", http.StatusConflict)
	}))
	defer daemon.Close()
	for _, args := range [][]string{
		{"register", "--port", "0", "api"},
		{"register", "--port", strconv.Itoa(closedPort), "api"},
		{"register", "--port", port, "api"},
		{"run", "api", "--", "/nonexistent/devproxy-child"},
		{"run", "api", "--", "sh", "-c", "exit 7"},
		{"run", "--ready-timeout", "10ms", "api", "--", "sleep", "30"},
		{"run", "api", "--", os.Args[0], "-test.run=^TestHelperReadinessChild$"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			args = append([]string{"--json", "--project", "example", "--worktree", "test", "--daemon", daemon.URL}, args...)
			cmd := readinessCLI(t, args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err == nil {
				t.Fatal("expected failure")
			}
			if strings.Contains(stdout.String(), "service_ready") || strings.Contains(stderr.String(), "service_ready") {
				t.Fatalf("failure emitted readiness: stdout=%s stderr=%s", &stdout, &stderr)
			}
			lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
			var result map[string]json.RawMessage
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &result); err != nil || result["error"] == nil {
				t.Fatalf("missing JSON error on stderr: %s", &stderr)
			}
		})
	}
}

func TestReadinessHumanAndWriteFailure(t *testing.T) {
	registration := devproxy.Registration{ID: "id", Project: "example", Worktree: "test", Service: "web", Route: "/", Port: 3000}
	var record bytes.Buffer
	if err := writeServiceReady(serviceFlags{asJSON: true, out: &record}, registration); err != nil {
		t.Fatal(err)
	}
	const wantRecord = `{"schema_version":1,"event":"service_ready","id":"id","project":"example","worktree":"test","service":"web","url":"https://example-test.localhost","route":"/","port":3000,"readiness":{"registration":"accepted","tcp":"listening","http_health":"not_checked"}}` + "\n"
	if record.String() != wantRecord {
		t.Fatalf("v1 wire contract changed: %s", &record)
	}
	for _, quiet := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		if err := writeServiceReady(serviceFlags{out: &stdout, errOut: &stderr, quiet: quiet}, registration); err != nil {
			t.Fatal(err)
		}
		want := "devproxy: web → https://example-test.localhost on port 3000\n"
		if quiet {
			want = ""
		}
		if stdout.Len() != 0 || stderr.String() != want {
			t.Fatalf("stdout=%q stderr=%q", &stdout, &stderr)
		}
	}
	if err := writeServiceReady(serviceFlags{asJSON: true, out: readinessBrokenWriter{}}, registration); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write failure lost: %v", err)
	}
}

type readinessBrokenWriter struct{}

func (readinessBrokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestReadinessClosedStdoutCleansUp(t *testing.T) {
	revoked := make(chan struct{}, 1)
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/unregister" {
			revoked <- struct{}{}
		}
	}))
	defer daemon.Close()
	pidFile := t.TempDir() + "/child.pid"
	cmd := readinessCLI(t, "run", "--json", "--project", "example", "--worktree", "test", "--daemon", daemon.URL,
		"web", "--", "sh", "-c", `exec "$@" >&2`, "child", os.Args[0], "-test.run=^TestHelperReadinessChild$")
	cmd.Env = append(cmd.Env, "DEVPROXY_READINESS_CHILD_PID="+pidFile)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close() // A real fd 1 EPIPE, not a synthetic io.Writer error.
	defer writer.Close()
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = writer, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	err = cmd.Wait()
	pidBytes, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("child did not start: %v; stderr=%s", readErr, &stderr)
	}
	pid, parseErr := strconv.Atoi(string(pidBytes))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	defer syscall.Kill(-pid, syscall.SIGKILL) // Avoid leaks even if the regression returns.
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(stderr.String(), "write readiness result") {
		t.Fatalf("expected handled write failure, got %v; stderr=%s", err, &stderr)
	}
	select {
	case <-revoked:
	default:
		t.Fatal("closed stdout bypassed unregister")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child %d survived readiness write failure: %v", pid, err)
	}
}
