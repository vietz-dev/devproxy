package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dreifach-ai/devproxy/internal/devproxy"
)

const defaultDaemon = "http://127.0.0.1:7654"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	case "register":
		err = register(os.Args[2:])
	case "list":
		err = list(os.Args[2:])
	case "help", "--help", "-h":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "devproxy:", err)
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  devproxy serve [--listen 127.0.0.1:7654] [--caddy-admin http://127.0.0.1:2019]
  devproxy run --project PROJECT [--route /api] [--url-env APP_URL] SERVICE -- COMMAND [ARGS...]
  devproxy register --project PROJECT --port PORT [--route /api] SERVICE
  devproxy list

Run and register derive the worktree name from the current git checkout.
The run command sets PORT and replaces {port} in command arguments.`)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:7654", "local control and dashboard address")
	admin := fs.String("caddy-admin", "http://127.0.0.1:2019", "Caddy admin API origin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("serve does not accept positional arguments")
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || host != "127.0.0.1" {
		return errors.New("serve must listen on 127.0.0.1")
	}
	server, err := devproxy.NewServer(*admin, *listen, "127.0.0.1")
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Serve(ctx)
}

type serviceFlags struct {
	project  *string
	worktree *string
	route    *string
	daemon   *string
	port     *int
}

func addServiceFlags(fs *flag.FlagSet, withPort bool) serviceFlags {
	f := serviceFlags{
		project:  fs.String("project", "", "stable project name"),
		worktree: fs.String("worktree", "", "worktree name (defaults to git root folder)"),
		route:    fs.String("route", "/", "HTTP path prefix"),
		daemon:   fs.String("daemon", defaultDaemon, "devproxy daemon origin"),
	}
	if withPort {
		f.port = fs.Int("port", 0, "already-running service port")
	}
	return f
}

func registrationFromFlags(f serviceFlags, name string, port int) (devproxy.Registration, error) {
	if *f.project == "" {
		return devproxy.Registration{}, errors.New("--project is required")
	}
	worktree := *f.worktree
	if worktree == "" {
		cmd := exec.Command("git", "rev-parse", "--show-toplevel")
		output, err := cmd.Output()
		if err != nil {
			return devproxy.Registration{}, errors.New("run inside a git worktree or pass --worktree")
		}
		worktree = filepath.Base(strings.TrimSpace(string(output)))
	}
	worktree = slug(worktree)
	if worktree == "" {
		return devproxy.Registration{}, errors.New("worktree name has no usable letters or digits")
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return devproxy.Registration{}, err
	}
	return devproxy.Registration{
		ID: hex.EncodeToString(idBytes), Project: *f.project,
		Worktree: worktree, Service: name, Route: *f.route, Port: port,
	}, nil
}

var nonLabel = regexp.MustCompile(`[^a-z0-9]+`)
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func slug(value string) string {
	return strings.Trim(nonLabel.ReplaceAllString(strings.ToLower(value), "-"), "-")
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	f := addServiceFlags(fs, false)
	readyTimeout := fs.Duration("ready-timeout", 60*time.Second, "time allowed for the service to start listening")
	urlEnv := fs.String("url-env", "", "also set this environment variable to the public URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 3 || rest[1] != "--" {
		return errors.New("expected SERVICE -- COMMAND [ARGS...]")
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	registration, err := registrationFromFlags(f, rest[0], port)
	if err != nil {
		return err
	}
	if *urlEnv != "" && !envNamePattern.MatchString(*urlEnv) {
		return errors.New("--url-env must be an environment variable name")
	}
	commandArgs := make([]string, len(rest[2:]))
	for i, arg := range rest[2:] {
		commandArgs[i] = strings.ReplaceAll(arg, "{port}", strconv.Itoa(port))
	}
	cmd := exec.Command(commandArgs[0], commandArgs[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(port), "DEVPROXY_URL="+publicURL(registration))
	if *urlEnv != "" {
		cmd.Env = append(cmd.Env, *urlEnv+"="+publicURL(registration))
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	deadline := time.NewTimer(*readyTimeout)
	defer deadline.Stop()
	probe := time.NewTicker(200 * time.Millisecond)
	defer probe.Stop()
	ready := false
	for !ready {
		select {
		case err := <-done:
			if err == nil {
				return errors.New("service exited before listening")
			}
			return fmt.Errorf("service exited before listening: %w", err)
		case sig := <-signals:
			return stopChild(cmd, done, sig)
		case <-deadline.C:
			_ = stopChild(cmd, done, syscall.SIGTERM)
			return errors.New("service did not start listening before --ready-timeout")
		case <-probe.C:
			connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 150*time.Millisecond)
			if err == nil {
				_ = connection.Close()
				ready = true
			}
		}
	}
	if err := announce(*f.daemon, registration); err != nil {
		_ = stopChild(cmd, done, syscall.SIGTERM)
		return err
	}
	defer revoke(*f.daemon, registration.ID)
	fmt.Printf("devproxy: %s → %s on port %d\n", registration.Service, publicURL(registration), port)
	return keepAlive(*f.daemon, registration, done, signals, cmd)
}

func register(args []string) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	f := addServiceFlags(fs, true)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || *f.port < 1 || *f.port > 65535 {
		return errors.New("expected --port PORT SERVICE")
	}
	registration, err := registrationFromFlags(f, fs.Arg(0), *f.port)
	if err != nil {
		return err
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*f.port)), time.Second)
	if err != nil {
		return fmt.Errorf("service is not listening on 127.0.0.1:%d: %w", *f.port, err)
	}
	_ = connection.Close()
	if err := announce(*f.daemon, registration); err != nil {
		return err
	}
	defer revoke(*f.daemon, registration.ID)
	fmt.Printf("devproxy: %s → %s on port %d\n", registration.Service, publicURL(registration), registration.Port)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	return keepAlive(*f.daemon, registration, nil, signals, nil)
}

func keepAlive(daemon string, registration devproxy.Registration, done <-chan error, signals <-chan os.Signal, cmd *exec.Cmd) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case sig := <-signals:
			if cmd != nil {
				return stopChild(cmd, done, sig)
			}
			return nil
		case <-ticker.C:
			status, err := request(daemon, "/v1/heartbeat", map[string]string{"id": registration.ID}, nil)
			if err != nil || status == http.StatusNotFound {
				if err := announce(daemon, registration); err != nil {
					fmt.Fprintln(os.Stderr, "devproxy: registration retry:", err)
				}
			}
		}
	}
}

func stopChild(cmd *exec.Cmd, done <-chan error, sig os.Signal) error {
	_ = cmd.Process.Signal(sig)
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return nil
	}
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func announce(daemon string, registration devproxy.Registration) error {
	status, err := request(daemon, "/v1/register", registration, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("registration returned HTTP %d", status)
	}
	return nil
}

func revoke(daemon, id string) {
	if _, err := request(daemon, "/v1/unregister", map[string]string{"id": id}, nil); err != nil {
		fmt.Fprintln(os.Stderr, "devproxy: unregister:", err)
	}
}

func publicURL(registration devproxy.Registration) string {
	return "https://" + registration.Project + "-" + registration.Worktree + ".localhost"
}

func request(daemon, path string, input, output any) (int, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Post(strings.TrimRight(daemon, "/")+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return response.StatusCode, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			return response.StatusCode, err
		}
	}
	return response.StatusCode, nil
}

func list(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	daemon := fs.String("daemon", defaultDaemon, "devproxy daemon origin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("list does not accept positional arguments")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(strings.TrimRight(*daemon, "/") + "/v1/services")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned HTTP %d", response.StatusCode)
	}
	var services []devproxy.Service
	if err := json.NewDecoder(response.Body).Decode(&services); err != nil {
		return err
	}
	for _, service := range services {
		fmt.Printf("%-42s %-12s %-16s %s\n", service.URL, service.Route, service.Service, net.JoinHostPort("127.0.0.1", strconv.Itoa(service.Port)))
	}
	return nil
}
