package devproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRegistrationBuildsSharedOriginRoutes(t *testing.T) {
	var mu sync.Mutex
	var latest string
	caddy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/load" || r.Header.Get("Content-Type") != "text/caddyfile" {
			t.Errorf("unexpected Caddy request: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		latest = string(body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer caddy.Close()
	server, err := NewServer(caddy.URL, "127.0.0.1:7654", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	inputs := []Registration{
		{ID: strings.Repeat("a", 32), Project: "kasta", Worktree: "ropefish", Service: "web", Route: "/", Port: 3000},
		{ID: strings.Repeat("b", 32), Project: "kasta", Worktree: "ropefish", Service: "api", Route: "/api", Port: 3001},
	}
	for _, input := range inputs {
		payload, _ := json.Marshal(input)
		response := httptest.NewRecorder()
		server.register(response, jsonRequest("/v1/register", payload))
		if response.Code != http.StatusOK {
			t.Fatalf("register %s: %d %s", input.Service, response.Code, response.Body.String())
		}
	}
	mu.Lock()
	config := latest
	mu.Unlock()
	if !strings.Contains(config, "https://kasta-ropefish.localhost") || !strings.Contains(config, "path /api /api/*") {
		t.Fatalf("missing host or API route:\n%s", config)
	}
	if strings.Index(config, "reverse_proxy 127.0.0.1:3001") > strings.Index(config, "reverse_proxy 127.0.0.1:3000") {
		t.Fatalf("API must precede catch-all web route:\n%s", config)
	}
	duplicate := inputs[1]
	duplicate.ID = strings.Repeat("c", 32)
	payload, _ := json.Marshal(duplicate)
	response := httptest.NewRecorder()
	server.register(response, jsonRequest("/v1/register", payload))
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate route: got %d, want 409", response.Code)
	}
}

func TestDashboardServesEmbeddedAssets(t *testing.T) {
	server := &Server{}
	page := httptest.NewRecorder()
	server.dashboard(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "<div id=\"root\"></div>") {
		t.Fatalf("dashboard response: %d %s", page.Code, page.Body.String())
	}
	if got := page.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("dashboard cache policy: %q", got)
	}
	wrongPath := httptest.NewRecorder()
	server.dashboard(wrongPath, httptest.NewRequest(http.MethodGet, "/not-a-page", nil))
	if wrongPath.Code != http.StatusNotFound {
		t.Fatalf("unknown dashboard path: got %d", wrongPath.Code)
	}
	assetNames, err := fs.ReadDir(dashboardFiles, "assets")
	if err != nil || len(assetNames) == 0 {
		t.Fatalf("embedded assets: %v", err)
	}
	assetPath := "/assets/" + assetNames[0].Name()
	asset := httptest.NewRecorder()
	http.FileServerFS(dashboardFiles).ServeHTTP(asset, httptest.NewRequest(http.MethodGet, assetPath, nil))
	if asset.Code != http.StatusOK || asset.Body.Len() == 0 {
		t.Fatalf("asset response: %d", asset.Code)
	}
}

func TestDashboardCaddyRouteAllowsOnlyReadPaths(t *testing.T) {
	config := renderCaddyfile(nil, "127.0.0.1:7654", "127.0.0.1", "127.0.0.1:2019")
	for _, path := range []string{"path /assets/*", "path /v1/services", "method GET"} {
		if !strings.Contains(config, path) {
			t.Fatalf("dashboard config missing %q:\n%s", path, config)
		}
	}
	for _, path := range []string{"/v1/register", "/v1/heartbeat", "/v1/unregister", "/v1/daemon"} {
		if strings.Contains(config, path) {
			t.Fatalf("dashboard exposes control path %q:\n%s", path, config)
		}
	}
}

func TestDockerDesktopCaddyConfig(t *testing.T) {
	server, err := NewServer("http://127.0.0.1:2019", "127.0.0.1:7654", "host.docker.internal")
	if err != nil {
		t.Fatal(err)
	}
	config := renderCaddyfile(nil, server.listen, server.upstreamHost, server.adminListen)
	for _, want := range []string{
		"admin 0.0.0.0:2019",
		"default_bind 0.0.0.0",
		"reverse_proxy host.docker.internal:7654",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("Docker Desktop config missing %q:\n%s", want, config)
		}
	}
}

func TestGeneratedCaddyfileAdapts(t *testing.T) {
	if os.Getenv("DEVPROXY_TEST_CADDY") == "" {
		t.Skip("set DEVPROXY_TEST_CADDY=1 to validate with Docker Caddy")
	}
	services := []Service{
		{Registration: Registration{Route: "/api", Port: 3001}, Host: "example-worktree.localhost"},
		{Registration: Registration{Route: "/", Port: 3000}, Host: "example-worktree.localhost"},
	}
	for name, upstreamHost := range map[string]string{
		"host network":   "127.0.0.1",
		"Docker Desktop": "host.docker.internal",
	} {
		t.Run(name, func(t *testing.T) {
			server, err := NewServer("http://127.0.0.1:2019", "127.0.0.1:7654", upstreamHost)
			if err != nil {
				t.Fatal(err)
			}
			config := renderCaddyfile(services, server.listen, server.upstreamHost, server.adminListen)
			cmd := exec.CommandContext(context.Background(), "docker", "run", "--rm", "-i", "caddy:2-alpine", "caddy", "adapt", "--config", "/dev/stdin", "--adapter", "caddyfile")
			cmd.Stdin = strings.NewReader(config)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Caddy rejected generated config: %v\n%s\n%s", err, config, output)
			}
			if bytes.Contains(output, []byte("not formatted")) {
				t.Fatalf("generated Caddyfile is not formatted:\n%s", config)
			}
		})
	}
}

func TestEmbeddedCaddyRoutesTraffic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Path))
	}))
	defer upstream.Close()
	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyPort := proxyListener.Addr().(*net.TCPAddr).Port
	_ = proxyListener.Close()
	services := []Service{{Registration: Registration{Route: "/api", Port: port}, Host: "example-worktree.localhost"}}
	configFor := func(routes []Service) string {
		config := renderCaddyfile(routes, upstream.Listener.Addr().String(), "127.0.0.1", "off")
		return strings.Replace(config, "\tadmin off", "\tadmin off\n\tskip_install_trust\n\thttps_port "+itoa(proxyPort), 1)
	}
	storageDir := filepath.Join(t.TempDir(), "devproxy data")
	if err := loadEmbeddedCaddy(configFor(nil), storageDir); err != nil {
		t.Fatal(err)
	}
	defer stopEmbeddedCaddy()
	if err := loadEmbeddedCaddy(configFor(services), storageDir); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "example-worktree.localhost"}}} // isolated test CA
	var response *http.Response
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		request, requestErr := http.NewRequest(http.MethodGet, "https://127.0.0.1:"+itoa(proxyPort)+"/api/ping", nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Host = "example-worktree.localhost"
		response, err = client.Do(request)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("embedded Caddy did not serve TLS: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "/api/ping" {
		t.Fatalf("embedded proxy returned %d %q", response.StatusCode, body)
	}
	for _, path := range []string{"/v1/register", "/v1/daemon/stop"} {
		blocked, err := http.NewRequest(http.MethodPost, "https://127.0.0.1:"+itoa(proxyPort)+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		blocked.Host = "devproxy.localhost"
		blockedResponse, err := client.Do(blocked)
		if err != nil {
			t.Fatal(err)
		}
		_ = blockedResponse.Body.Close()
		if blockedResponse.StatusCode != http.StatusNotFound {
			t.Fatalf("public %s returned %d, want 404", path, blockedResponse.StatusCode)
		}
	}
}

func TestCaddyRoutesTraffic(t *testing.T) {
	if os.Getenv("DEVPROXY_TEST_CADDY") == "" {
		t.Skip("set DEVPROXY_TEST_CADDY=1 to test routing with Docker host networking")
	}
	dashboard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("dashboard:" + r.URL.Path))
	}))
	defer dashboard.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("api:" + r.URL.Path))
	}))
	defer api.Close()
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("web:" + r.URL.Path))
	}))
	defer web.Close()
	apiPort := api.Listener.Addr().(*net.TCPAddr).Port
	webPort := web.Listener.Addr().(*net.TCPAddr).Port
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpsPort := portListener.Addr().(*net.TCPAddr).Port
	_ = portListener.Close()
	services := []Service{
		{Registration: Registration{Route: "/", Port: webPort}, Host: "example-worktree.localhost"},
		{Registration: Registration{Route: "/api", Port: apiPort}, Host: "example-worktree.localhost"},
	}
	config := renderCaddyfile(services, dashboard.Listener.Addr().String(), "127.0.0.1", "localhost:2019")
	config = strings.Replace(config, "\tadmin localhost:2019", "\tadmin off\n\thttps_port "+itoa(httpsPort), 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := "devproxy-test-" + itoa(time.Now().UnixNano())
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "host", "--name", name, "-i", "caddy:2-alpine", "caddy", "run", "--config", "/dev/stdin", "--adapter", "caddyfile")
	cmd.Stdin = strings.NewReader(config)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
		_ = cmd.Wait()
	}()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "example-worktree.localhost"}}} // local test CA
	base := "https://127.0.0.1:" + itoa(httpsPort)
	var ready bool
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); {
		connection, err := net.DialTimeout("tcp", "127.0.0.1:"+itoa(httpsPort), 200*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			ready = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("Caddy did not start:\n%s", output.String())
	}
	for path, want := range map[string]string{"/api/ping": "api:/api/ping", "/api": "api:/api", "/home": "web:/home"} {
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		req.Host = "example-worktree.localhost"
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v\nCaddy:\n%s", path, err, output.String())
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK || string(body) != want {
			t.Fatalf("GET %s: got %d %q, want 200 %q\nCaddy:\n%s", path, res.StatusCode, body, want, output.String())
		}
	}
	for _, check := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/", http.StatusOK},
		{http.MethodGet, "/v1/services", http.StatusOK},
		{http.MethodPost, "/v1/register", http.StatusNotFound},
		{http.MethodPost, "/v1/heartbeat", http.StatusNotFound},
		{http.MethodPost, "/v1/unregister", http.StatusNotFound},
	} {
		req, _ := http.NewRequest(check.method, base+check.path, nil)
		req.Host = "devproxy.localhost"
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("dashboard %s %s: %v", check.method, check.path, err)
		}
		_ = res.Body.Close()
		if res.StatusCode != check.status {
			t.Fatalf("dashboard %s %s: got %d, want %d", check.method, check.path, res.StatusCode, check.status)
		}
	}
}

func itoa(value any) string { return fmt.Sprint(value) }

func jsonRequest(path string, payload []byte) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	return request
}
