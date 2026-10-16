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
	"unsafe"

	"github.com/vietz-dev/devproxy/internal/devproxy"
)

const defaultDaemon = "http://127.0.0.1:7654"

func main() {
	if os.Getenv("DEVPROXY_SIGNAL_RELAY") == "1" {
		signalRelay()
		return
	}
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
		var exit interface{ ExitCode() int }
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(port), "DEVPROXY_URL="+publicURL(registration))
	if *urlEnv != "" {
		cmd.Env = append(cmd.Env, *urlEnv+"="+publicURL(registration))
	}
	terminal := foregroundChild(cmd)
	defer terminal.restore()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGTSTP)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	stopped := make(chan struct{}, 1)
	go waitChild(cmd, done, stopped)
	interrupts := make(chan struct{}, 1)
	if terminal.hasTTY {
		// Terminal SIGINT reaches only the foreground child group. A relay in
		// that group lets the supervisor enforce its shutdown timeout too.
		if err := startSignalRelay(cmd.Process.Pid, interrupts); err != nil {
			_ = stopChild(cmd, done, syscall.SIGTERM)
			return err
		}
	}
	deadline := time.NewTimer(*readyTimeout)
	defer deadline.Stop()
	probe := time.NewTicker(200 * time.Millisecond)
	defer probe.Stop()
	ready := false
	for !ready {
		select {
		case err := <-done:
			finishProcessGroup(cmd)
			if err == nil {
				return errors.New("service exited before listening")
			}
			return fmt.Errorf("service exited before listening: %w", err)
		case sig := <-signals:
			if sig == syscall.SIGTSTP {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTSTP)
				continue
			}
			return stopChild(cmd, done, sig)
		case <-interrupts:
			return stopChild(cmd, done, nil)
		case <-stopped:
			terminal.suspendAndResume(cmd)
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
	announceCtx, cancelAnnounce := context.WithCancel(context.Background())
	defer cancelAnnounce()
	announced := make(chan error, 1)
	go func() { announced <- announceContext(announceCtx, *f.daemon, registration) }()
registrationLoop:
	for {
		select {
		case err := <-announced:
			if err != nil {
				_ = stopChild(cmd, done, syscall.SIGTERM)
				return err
			}
			break registrationLoop
		case err := <-done:
			cancelAnnounce()
			finishProcessGroup(cmd)
			revoke(*f.daemon, registration.ID)
			return err
		case sig := <-signals:
			if sig == syscall.SIGTSTP {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTSTP)
				continue
			}
			cancelAnnounce()
			err := stopChild(cmd, done, sig)
			revoke(*f.daemon, registration.ID)
			return err
		case <-interrupts:
			cancelAnnounce()
			err := stopChild(cmd, done, nil)
			revoke(*f.daemon, registration.ID)
			return err
		case <-stopped:
			terminal.suspendAndResume(cmd)
		}
	}
	cancelAnnounce()
	defer revoke(*f.daemon, registration.ID)
	fmt.Printf("devproxy: %s → %s on port %d\n", registration.Service, publicURL(registration), port)
	return keepAliveWithStops(*f.daemon, registration, done, signals, cmd, stopped, interrupts, terminal)
}

type terminalHandoff struct {
	fd          uintptr
	parentGroup int32
	hasTTY      bool
	active      bool
}

func foregroundChild(cmd *exec.Cmd) *terminalHandoff {
	fd := os.Stdin.Fd()
	foregroundGroup, ok := terminalForegroundGroup(fd)
	terminal := &terminalHandoff{fd: fd, parentGroup: foregroundGroup, hasTTY: ok}
	if !ok || int(foregroundGroup) != syscall.Getpgrp() {
		return terminal
	}
	// The child must own the terminal while it reads stdin. The parent needs
	// SIGTTOU ignored so it can restore its process group on exit.
	signal.Ignore(syscall.SIGTTOU)
	terminal.active = true
	cmd.SysProcAttr.Foreground = true
	cmd.SysProcAttr.Ctty = int(fd)
	return terminal
}

func (t *terminalHandoff) restore() {
	if !t.active {
		return
	}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, t.fd, uintptr(syscall.TIOCSPGRP), uintptr(unsafe.Pointer(&t.parentGroup)))
	signal.Reset(syscall.SIGTTOU)
	t.active = false
}

func (t *terminalHandoff) suspendAndResume(cmd *exec.Cmd) {
	t.restore()
	// A shell job may contain a wrapper or pipeline peers in our group.
	// Stop the whole job, but never stop the terminal session's own group.
	sessionID, _, errno := syscall.Syscall(syscall.SYS_GETSID, 0, 0, 0)
	if errno == 0 && uintptr(syscall.Getpgrp()) != sessionID {
		_ = syscall.Kill(-syscall.Getpgrp(), syscall.SIGSTOP)
	} else {
		_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP)
	}
	if group, ok := terminalForegroundGroup(t.fd); ok && int(group) == syscall.Getpgrp() {
		signal.Ignore(syscall.SIGTTOU)
		childGroup := int32(cmd.Process.Pid)
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, t.fd, uintptr(syscall.TIOCSPGRP), uintptr(unsafe.Pointer(&childGroup)))
		t.active = true
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGCONT)
}

func terminalForegroundGroup(fd uintptr) (int32, bool) {
	var group int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGPGRP), uintptr(unsafe.Pointer(&group)))
	return group, errno == 0
}

func signalRelay() {
	pipe := os.NewFile(3, "devproxy-signal-relay")
	defer pipe.Close()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if _, err := pipe.Write([]byte{'R'}); err != nil {
		return
	}
	for sig := range signals {
		if sig == syscall.SIGTERM {
			return
		}
		if _, err := pipe.Write([]byte{'I'}); err != nil {
			return
		}
	}
}

func startSignalRelay(group int, interrupts chan<- struct{}) error {
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	relay := exec.Command(os.Args[0])
	relay.Env = append(os.Environ(), "DEVPROXY_SIGNAL_RELAY=1")
	relay.ExtraFiles = []*os.File{writer}
	relay.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: group}
	if err := relay.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return err
	}
	_ = writer.Close()
	ready := make(chan error, 1)
	go func() {
		defer reader.Close()
		defer relay.Wait()
		var byte [1]byte
		starting := true
		for {
			if _, err := reader.Read(byte[:]); err != nil {
				if starting {
					ready <- err
				}
				return
			}
			if starting {
				starting = false
				if byte[0] != 'R' {
					ready <- errors.New("signal relay sent an invalid startup message")
					return
				}
				ready <- nil
				continue
			}
			if byte[0] != 'I' {
				continue
			}
			select {
			case interrupts <- struct{}{}:
			default:
			}
		}
	}()
	select {
	case err := <-ready:
		return err
	case <-time.After(time.Second):
		return errors.New("signal relay did not start")
	}
}

type childExitError struct{ status syscall.WaitStatus }

func (e childExitError) Error() string {
	if e.status.Signaled() {
		return fmt.Sprintf("child terminated by %s", e.status.Signal())
	}
	return fmt.Sprintf("exit status %d", e.status.ExitStatus())
}

func (e childExitError) ExitCode() int {
	if e.status.Signaled() {
		return 128 + int(e.status.Signal())
	}
	return e.status.ExitStatus()
}

func waitChild(cmd *exec.Cmd, done chan<- error, stopped chan<- struct{}) {
	for {
		var status syscall.WaitStatus
		_, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WUNTRACED, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			_ = cmd.Wait()
			done <- err
			return
		}
		if status.Stopped() {
			select {
			case stopped <- struct{}{}:
			default:
			}
			continue
		}
		_ = cmd.Wait() // Release os/exec resources after Wait4 reaps the child.
		if status.Exited() && status.ExitStatus() == 0 {
			done <- nil
		} else {
			done <- childExitError{status: status}
		}
		return
	}
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
	return keepAliveWithTicks(daemon, registration, done, signals, cmd, ticker.C)
}

func keepAliveWithTicks(daemon string, registration devproxy.Registration, done <-chan error, signals <-chan os.Signal, cmd *exec.Cmd, ticks <-chan time.Time) error {
	return keepAliveLoop(daemon, registration, done, signals, cmd, nil, nil, nil, ticks)
}

func keepAliveWithStops(daemon string, registration devproxy.Registration, done <-chan error, signals <-chan os.Signal, cmd *exec.Cmd, stopped <-chan struct{}, interrupts <-chan struct{}, terminal *terminalHandoff) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	return keepAliveLoop(daemon, registration, done, signals, cmd, stopped, interrupts, terminal, ticker.C)
}

func keepAliveLoop(daemon string, registration devproxy.Registration, done <-chan error, signals <-chan os.Signal, cmd *exec.Cmd, stopped <-chan struct{}, interrupts <-chan struct{}, terminal *terminalHandoff, ticks <-chan time.Time) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	heartbeatDone := make(chan struct{}, 1)
	heartbeatRunning := false
	for {
		select {
		case err := <-done:
			cancel()
			if cmd != nil {
				finishProcessGroup(cmd)
			}
			return err
		case sig := <-signals:
			if sig == syscall.SIGTSTP && cmd != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTSTP)
				continue
			}
			cancel()
			if cmd != nil {
				return stopChild(cmd, done, sig)
			}
			return nil
		case <-interrupts:
			cancel()
			return stopChild(cmd, done, nil)
		case <-stopped:
			terminal.suspendAndResume(cmd)
		case <-heartbeatDone:
			heartbeatRunning = false
		case <-ticks:
			if heartbeatRunning {
				continue
			}
			heartbeatRunning = true
			go func() {
				defer func() { heartbeatDone <- struct{}{} }()
				status, err := requestContext(ctx, daemon, "/v1/heartbeat", map[string]string{"id": registration.ID}, nil)
				if ctx.Err() == nil && (err != nil || status == http.StatusNotFound) {
					if err := announceContext(ctx, daemon, registration); err != nil && ctx.Err() == nil {
						fmt.Fprintln(os.Stderr, "devproxy: registration retry:", err)
					}
				}
			}()
		}
	}
}

func stopChild(cmd *exec.Cmd, done <-chan error, sig os.Signal) error {
	if signal, ok := sig.(syscall.Signal); ok {
		_ = syscall.Kill(-cmd.Process.Pid, signal)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
	finishProcessGroup(cmd)
	return nil
}

func finishProcessGroup(cmd *exec.Cmd) {
	pid := cmd.Process.Pid
	if syscall.Kill(-pid, 0) == syscall.ESRCH {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if syscall.Kill(-pid, 0) == syscall.ESRCH {
			return
		}
		select {
		case <-deadline.C:
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			return
		case <-ticker.C:
		}
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
	return announceContext(context.Background(), daemon, registration)
}

func announceContext(ctx context.Context, daemon string, registration devproxy.Registration) error {
	status, err := requestContext(ctx, daemon, "/v1/register", registration, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("registration returned HTTP %d", status)
	}
	return nil
}

func revoke(daemon, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := requestContext(ctx, daemon, "/v1/unregister", map[string]string{"id": id}, nil); err != nil {
		fmt.Fprintln(os.Stderr, "devproxy: unregister:", err)
	}
}

func publicURL(registration devproxy.Registration) string {
	return "https://" + registration.Project + "-" + registration.Worktree + ".localhost"
}

func request(daemon, path string, input, output any) (int, error) {
	return requestContext(context.Background(), daemon, path, input, output)
}

func requestContext(ctx context.Context, daemon, path string, input, output any) (int, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(daemon, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
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
