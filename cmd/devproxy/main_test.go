package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dreifach-ai/devproxy/internal/devproxy"
)

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
	if err := run([]string{"--project", "example", "--worktree", "test", "--daemon", "http://" + listen, "web", "--", os.Args[0], "-test.run=TestHelperHTTPService"}); err != nil {
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
