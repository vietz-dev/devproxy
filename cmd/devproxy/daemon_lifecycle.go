package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type daemonState struct {
	PID    int    `json:"pid"`
	Listen string `json:"listen"`
}

var lifecycleClient = &http.Client{Timeout: 2 * time.Second}

func localDaemonURL(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", usage("daemon must be an HTTP origin on 127.0.0.1", "devproxy status --daemon http://127.0.0.1:7654")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func readDaemon(origin string) (daemonState, error) {
	response, err := lifecycleClient.Get(origin + "/v1/daemon")
	if err != nil {
		return daemonState{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return daemonState{}, fmt.Errorf("daemon returned HTTP %d", response.StatusCode)
	}
	var state daemonState
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&state); err != nil {
		return daemonState{}, err
	}
	if state.PID <= 0 || state.Listen == "" {
		return daemonState{}, errors.New("invalid daemon status response")
	}
	return state, nil
}

func startBackground(listen, admin string, dockerDesktop bool, out io.Writer) error {
	host, port, err := net.SplitHostPort(listen)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || host != "127.0.0.1" || portErr != nil || portNumber < 1 || portNumber > 65535 {
		return usage("start must listen on 127.0.0.1", "devproxy start --listen 127.0.0.1:7654")
	}
	origin := "http://" + listen
	if state, err := readDaemon(origin); err == nil {
		fmt.Fprintf(out, "devproxy is already running (PID %d) at %s\n", state.PID, origin)
		return nil
	}
	if dockerDesktop && admin == "" {
		return usage("--docker-desktop requires --caddy-admin", "devproxy start --docker-desktop --caddy-admin http://127.0.0.1:2019")
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return fmt.Errorf("find user cache directory: %w", err)
	}
	logDir := filepath.Join(cacheDir, "devproxy")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	logPath := filepath.Join(logDir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	executable, err := os.Executable()
	if err != nil {
		_ = writer.Close()
		return err
	}
	args := []string{"serve", "--listen=" + listen, "--caddy-admin=" + admin, "--docker-desktop=" + strconv.FormatBool(dockerDesktop)}
	child := exec.Command(executable, args...)
	child.Dir = logDir
	child.Env = append(os.Environ(), "DEVPROXY_READY_FD=3")
	child.Stdin = nil
	child.Stdout, child.Stderr = logFile, logFile
	child.ExtraFiles = []*os.File{writer}
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		_ = writer.Close()
		return fmt.Errorf("start daemon: %w", err)
	}
	_ = writer.Close()
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(reader).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "ready\n" {
			waitErr := child.Wait()
			return fmt.Errorf("daemon failed to start (%v); see %s", waitErr, logPath)
		}
	case <-time.After(30 * time.Second):
		_ = child.Process.Kill()
		_ = child.Wait()
		return fmt.Errorf("daemon did not become ready within 30s; see %s", logPath)
	}
	childPID := child.Process.Pid
	for i := 0; i < 20; i++ {
		if state, err := readDaemon(origin); err == nil && state.PID == childPID {
			if err := child.Process.Release(); err != nil {
				return err
			}
			fmt.Fprintf(out, "devproxy started (PID %d) at %s\nLog: %s\n", state.PID, origin, logPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	return fmt.Errorf("daemon started but control API did not respond; see %s", logPath)
}

func statusBackground(daemon string, asJSON bool, out io.Writer) error {
	origin, err := localDaemonURL(daemon)
	if err != nil {
		return err
	}
	state, err := readDaemon(origin)
	if err != nil {
		return fmt.Errorf("devproxy is not running at %s: %w", origin, err)
	}
	if asJSON {
		return json.NewEncoder(out).Encode(state)
	}
	_, err = fmt.Fprintf(out, "devproxy is running (PID %d) at %s\n", state.PID, origin)
	return err
}

func stopBackground(daemon string, out io.Writer) error {
	origin, err := localDaemonURL(daemon)
	if err != nil {
		return err
	}
	state, err := readDaemon(origin)
	if err != nil {
		return fmt.Errorf("devproxy is not running at %s: %w", origin, err)
	}
	request, err := http.NewRequest(http.MethodPost, origin+"/v1/daemon/stop", strings.NewReader("{}"))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := lifecycleClient.Do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("daemon returned HTTP %s", strconv.Itoa(response.StatusCode))
	}
	for i := 0; i < 100; i++ {
		if errors.Is(syscall.Kill(state.PID, 0), syscall.ESRCH) {
			_, err = fmt.Fprintf(out, "devproxy stopped (PID %d)\n", state.PID)
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("devproxy (PID %d) did not stop within 10s", state.PID)
}
