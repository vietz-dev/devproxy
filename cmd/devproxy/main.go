package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	"github.com/dreifach-ai/devproxy/internal/devproxy"
	"github.com/spf13/afero"
)

const defaultDaemon = "http://127.0.0.1:7654"

func main() {
	if os.Getenv("DEVPROXY_SIGNAL_RELAY") == "1" {
		signalRelay()
		return
	}
	root, config := newRootCommandWithConfig(afero.NewOsFs())
	err := root.Execute()
	if err != nil {
		renderCLIError(os.Stderr, err, errorAsJSONAfterExecution(root, config, os.Args[1:]))
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		if strings.HasPrefix(err.Error(), "unknown command ") {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func serve(listen, admin string, dockerDesktop bool) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil || host != "127.0.0.1" {
		return usage("serve must listen on 127.0.0.1", "devproxy serve --listen 127.0.0.1:7654")
	}
	if dockerDesktop && admin == "" {
		return usage("--docker-desktop requires --caddy-admin", "devproxy serve --docker-desktop --caddy-admin http://127.0.0.1:2019")
	}
	var server *devproxy.Server
	if admin == "" {
		server, err = devproxy.NewEmbeddedServer(listen)
	} else {
		upstreamHost := "127.0.0.1"
		if dockerDesktop {
			upstreamHost = "host.docker.internal"
		}
		server, err = devproxy.NewServer(admin, listen, upstreamHost)
		if err != nil {
			return usage(err.Error(), "devproxy serve --caddy-admin http://127.0.0.1:2019")
		}
	}
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Getenv("DEVPROXY_READY_FD") == "3" {
		ready := os.NewFile(3, "devproxy-ready")
		defer ready.Close()
		return server.ServeReady(ctx, func() { _, _ = ready.Write([]byte("ready\n")) })
	}
	return server.Serve(ctx)
}

func registrationFromFlags(f serviceFlags, name string, port int) (devproxy.Registration, error) {
	if *f.project == "" {
		return devproxy.Registration{}, usage("--project is required", "devproxy run --project PROJECT SERVICE -- COMMAND")
	}
	worktree := *f.worktree
	if worktree == "" {
		cmd := exec.Command("git", "rev-parse", "--show-toplevel")
		output, err := cmd.Output()
		if err != nil {
			return devproxy.Registration{}, usage("run inside a git worktree or pass --worktree", "devproxy run --worktree NAME --project PROJECT SERVICE -- COMMAND")
		}
		worktree = filepath.Base(strings.TrimSpace(string(output)))
	}
	worktree = slug(worktree)
	if worktree == "" {
		return devproxy.Registration{}, usage("worktree name has no usable letters or digits", "devproxy run --worktree NAME --project PROJECT SERVICE -- COMMAND")
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

func runService(f serviceFlags, readyTimeout time.Duration, urlEnv, name string, args []string) error {
	port, err := freePort()
	if err != nil {
		return err
	}
	registration, err := registrationFromFlags(f, name, port)
	if err != nil {
		return err
	}
	if urlEnv != "" && !envNamePattern.MatchString(urlEnv) {
		return usage("--url-env must be an environment variable name", "devproxy run --url-env PUBLIC_URL --project PROJECT SERVICE -- COMMAND")
	}
	commandArgs := make([]string, len(args))
	for i, arg := range args {
		commandArgs[i] = strings.ReplaceAll(arg, "{port}", strconv.Itoa(port))
	}
	cmd := exec.Command(commandArgs[0], commandArgs[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(port), "DEVPROXY_URL="+publicURL(registration))
	if urlEnv != "" {
		cmd.Env = append(cmd.Env, urlEnv+"="+publicURL(registration))
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
	deadline := time.NewTimer(readyTimeout)
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
	if !f.quiet {
		fmt.Fprintf(os.Stderr, "devproxy: %s → %s on port %d\n", registration.Service, publicURL(registration), port)
	}
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

func registerService(f serviceFlags, name string) error {
	if *f.port < 1 || *f.port > 65535 {
		return usage("expected --port PORT SERVICE", "devproxy register --port PORT --project PROJECT SERVICE")
	}
	registration, err := registrationFromFlags(f, name, *f.port)
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
	if !f.quiet {
		fmt.Fprintf(os.Stderr, "devproxy: %s → %s on port %d\n", registration.Service, publicURL(registration), registration.Port)
	}
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

func listServices(daemon string, asJSON bool, out io.Writer) error {
	return listServicesFormatted(daemon, asJSON, false, out)
}

func listServicesFormatted(daemon string, asJSON, plain bool, out io.Writer) error {
	services, err := fetchServices(daemon)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(out).Encode(services)
	}
	for _, service := range services {
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(service.Port))
		if plain {
			fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", plainField(service.URL), plainField(service.Route), plainField(service.Service), address)
		} else {
			fmt.Fprintf(out, "%-42s %-12s %-16s %s\n", service.URL, service.Route, service.Service, address)
		}
	}
	return nil
}

var plainEscaper = strings.NewReplacer("\\", "\\\\", "\t", "\\t", "\r", "\\r", "\n", "\\n")

func plainField(value string) string { return plainEscaper.Replace(value) }

func inspectService(daemon string, asJSON bool, route, query string, out io.Writer) error {
	if route != "" && !strings.HasPrefix(route, "/") {
		return usage("--route must begin with /", "devproxy inspect --route /api HOST")
	}
	services, err := fetchServices(daemon)
	if err != nil {
		return err
	}
	var matches []devproxy.Service
	for _, service := range services {
		if (service.ID == query || service.Host == query || service.URL == query) && (route == "" || service.Route == route) {
			matches = append(matches, service)
		}
	}
	if len(matches) == 0 {
		return notFound(fmt.Sprintf("no active registration matches %q", query), "devproxy list")
	}
	if len(matches) > 1 {
		return conflict(fmt.Sprintf("%q matches %d registrations; pass --route or use a registration ID", query, len(matches)), "devproxy inspect --route /api "+query)
	}
	service := matches[0]
	if asJSON {
		return json.NewEncoder(out).Encode(service)
	}
	fmt.Fprintf(out, "ID:       %s\nProject:  %s\nWorktree: %s\nService:  %s\nURL:      %s\nHost:     %s\nRoute:    %s\nPort:     %d\nExpires:  %s\n",
		service.ID, service.Project, service.Worktree, service.Service, service.URL, service.Host, service.Route, service.Port, service.ExpiresAt.Format(time.RFC3339))
	return nil
}

func fetchServices(daemon string) ([]devproxy.Service, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(strings.TrimRight(daemon, "/") + "/v1/services")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon returned HTTP %d", response.StatusCode)
	}
	var services []devproxy.Service
	if err := json.NewDecoder(response.Body).Decode(&services); err != nil {
		return nil, err
	}
	if services == nil {
		services = []devproxy.Service{}
	}
	return services, nil
}
