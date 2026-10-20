package devproxy

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed web/dist
var dashboardAssets embed.FS

var dashboardFiles = func() fs.FS {
	files, err := fs.Sub(dashboardAssets, "web/dist")
	if err != nil {
		panic(err)
	}
	return files
}()

const LeaseDuration = 20 * time.Second

var labelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var routePattern = regexp.MustCompile(`^/(?:[a-zA-Z0-9_-]+(?:/[a-zA-Z0-9_-]+)*)?$`)

type Registration struct {
	ID       string `json:"id"`
	Project  string `json:"project"`
	Worktree string `json:"worktree"`
	Service  string `json:"service"`
	Route    string `json:"route"`
	Port     int    `json:"port"`
}

type Service struct {
	Registration
	Host      string    `json:"host"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Server struct {
	mu           sync.Mutex
	services     map[string]Service
	adminURL     string
	adminListen  string
	listen       string
	upstreamHost string
	client       *http.Client
	loadCaddy    func(string) error
	stopCaddy    func() error
}

// NewEmbeddedServer runs Caddy in this process and keeps its admin API disabled.
func NewEmbeddedServer(listen string) (*Server, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("find devproxy data directory: %w", err)
	}
	s, err := NewServer("http://127.0.0.1:2019", listen, "127.0.0.1")
	if err != nil {
		return nil, err
	}
	s.adminListen = "off"
	storageDir := filepath.Join(configDir, "devproxy", "caddy")
	s.loadCaddy = func(config string) error { return loadEmbeddedCaddy(config, storageDir) }
	s.stopCaddy = stopEmbeddedCaddy
	return s, nil
}

func NewServer(adminURL, listen, upstreamHost string) (*Server, error) {
	parsed, err := url.Parse(adminURL)
	if err != nil || parsed.Scheme != "http" || parsed.Port() == "" || parsed.Path != "" || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		return nil, errors.New("Caddy admin URL must be an HTTP origin, for example http://127.0.0.1:2019")
	}
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return nil, fmt.Errorf("invalid listen address: %w", err)
	}
	if upstreamHost != "127.0.0.1" && upstreamHost != "localhost" && upstreamHost != "host.docker.internal" {
		return nil, errors.New("upstream host must be 127.0.0.1, localhost, or host.docker.internal")
	}
	adminListen := parsed.Host
	if upstreamHost == "host.docker.internal" {
		adminListen = net.JoinHostPort("0.0.0.0", parsed.Port())
	}
	return &Server{
		services:     make(map[string]Service),
		adminURL:     strings.TrimRight(adminURL, "/"),
		adminListen:  adminListen,
		listen:       listen,
		upstreamHost: upstreamHost,
		client:       &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (s *Server) Serve(ctx context.Context) error {
	return s.ServeReady(ctx, nil)
}

// ServeReady calls ready after Caddy and the control listener are ready.
func (s *Server) ServeReady(ctx context.Context, ready func()) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	if s.stopCaddy != nil {
		defer s.stopCaddy()
	}
	// A restarted daemon must first remove registrations left in Caddy by the
	// previous process. Clients will register themselves again on heartbeat.
	s.mu.Lock()
	err = s.syncCaddy(ctx)
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("connect to Caddy: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.dashboard)
	mux.Handle("GET /assets/", http.FileServerFS(dashboardFiles))
	mux.HandleFunc("GET /v1/services", s.list)
	mux.HandleFunc("GET /v1/daemon", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"pid": os.Getpid(), "listen": s.listen})
	})
	mux.HandleFunc("POST /v1/daemon/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		go func() {
			<-r.Context().Done()
			cancel()
		}()
	})
	mux.HandleFunc("POST /v1/register", s.register)
	mux.HandleFunc("POST /v1/heartbeat", s.heartbeat)
	mux.HandleFunc("POST /v1/unregister", s.unregister)
	server := &http.Server{Addr: s.listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go s.reconcile(ctx)
	slog.Info("devproxy ready", "listen", s.listen, "dashboard", "https://devproxy.localhost")
	if ready != nil {
		ready()
	}
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		<-shutdownDone
		return nil
	}
	return err
}

func (s *Server) reconcile(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			for id, service := range s.services {
				if time.Now().After(service.ExpiresAt) {
					delete(s.services, id)
				}
			}
			err := s.syncCaddy(ctx)
			s.mu.Unlock()
			if err != nil && ctx.Err() == nil {
				slog.Warn("Caddy sync failed; retrying", "error", err)
			}
		}
	}
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var input Registration
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validate(input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	host := input.Project + "-" + input.Worktree + ".localhost"
	service := Service{
		Registration: input,
		Host:         host,
		URL:          "https://" + host,
		ExpiresAt:    time.Now().Add(LeaseDuration),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, existing := range s.services {
		if id != input.ID && existing.Host == host && existing.Route == input.Route {
			http.Error(w, "route is already registered", http.StatusConflict)
			return
		}
	}
	previous, existed := s.services[input.ID]
	s.services[input.ID] = service
	if err := s.syncCaddy(r.Context()); err != nil {
		if existed {
			s.services[input.ID] = previous
		} else {
			delete(s.services, input.ID)
		}
		http.Error(w, "Caddy rejected registration: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, service)
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	s.mu.Lock()
	service, ok := s.services[input.ID]
	if ok {
		service.ExpiresAt = time.Now().Add(LeaseDuration)
		s.services[input.ID] = service
	}
	s.mu.Unlock()
	if !ok {
		http.Error(w, "registration not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unregister(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	s.mu.Lock()
	delete(s.services, input.ID)
	err := s.syncCaddy(r.Context())
	s.mu.Unlock()
	if err != nil {
		http.Error(w, "registration removed; Caddy update will be retried: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) list(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	services := s.sortedServices()
	s.mu.Unlock()
	writeJSON(w, services)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFileFS(w, r, dashboardFiles, "index.html")
}

func (s *Server) sortedServices() []Service {
	services := make([]Service, 0, len(s.services))
	for _, service := range s.services {
		services = append(services, service)
	}
	sort.Slice(services, func(i, j int) bool {
		if services[i].Host == services[j].Host {
			return services[i].Route < services[j].Route
		}
		return services[i].Host < services[j].Host
	})
	return services
}

func (s *Server) syncCaddy(ctx context.Context) error {
	config := renderCaddyfile(s.sortedServices(), s.listen, s.upstreamHost, s.adminListen)
	if s.loadCaddy != nil {
		return s.loadCaddy(config)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.adminURL+"/load", strings.NewReader(config))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/caddyfile")
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("Caddy HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func renderCaddyfile(services []Service, listen, upstreamHost, adminListen string) string {
	_, port, _ := net.SplitHostPort(listen)
	bindHost := "127.0.0.1"
	if upstreamHost == "host.docker.internal" {
		bindHost = "0.0.0.0"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "{\n\tadmin %s\n\tlocal_certs\n\tdefault_bind %s\n\tauto_https disable_redirects\n}\n\n", adminListen, bindHost)
	fmt.Fprintf(&b, "https://devproxy.localhost {\n\ttls internal\n\troute {\n\t\t@dashboard {\n\t\t\tpath /\n\t\t\tmethod GET\n\t\t}\n\t\t@assets {\n\t\t\tpath /assets/*\n\t\t\tmethod GET\n\t\t}\n\t\t@services {\n\t\t\tpath /v1/services\n\t\t\tmethod GET\n\t\t}\n\t\thandle @dashboard {\n\t\t\treverse_proxy %s:%s\n\t\t}\n\t\thandle @assets {\n\t\t\treverse_proxy %s:%s\n\t\t}\n\t\thandle @services {\n\t\t\treverse_proxy %s:%s\n\t\t}\n\t\trespond 404\n\t}\n}\n", upstreamHost, port, upstreamHost, port, upstreamHost, port)
	for start := 0; start < len(services); {
		end := start + 1
		for end < len(services) && services[end].Host == services[start].Host {
			end++
		}
		fmt.Fprintf(&b, "\nhttps://%s {\n\ttls internal\n", services[start].Host)
		routeIDs := make(map[string]int)
		for i := start; i < end; i++ {
			if services[i].Route != "/" {
				routeIDs[services[i].Route] = i
				fmt.Fprintf(&b, "\t@route%d path %s %s/*\n", i, services[i].Route, services[i].Route)
			}
		}
		b.WriteString("\troute {\n")
		// Longer routes must be considered first, independent of registration order.
		group := append([]Service(nil), services[start:end]...)
		sort.SliceStable(group, func(i, j int) bool { return len(group[i].Route) > len(group[j].Route) })
		for _, service := range group {
			if service.Route == "/" {
				fmt.Fprintf(&b, "\t\thandle {\n\t\t\treverse_proxy %s:%d {\n\t\t\t\tstream_close_delay 5m\n\t\t\t}\n\t\t}\n", upstreamHost, service.Port)
			} else {
				fmt.Fprintf(&b, "\t\thandle @route%d {\n\t\t\treverse_proxy %s:%d {\n\t\t\t\tstream_close_delay 5m\n\t\t\t}\n\t\t}\n", routeIDs[service.Route], upstreamHost, service.Port)
			}
		}
		b.WriteString("\t\trespond 404\n\t}\n}\n")
		start = end
	}
	return b.String()
}

func validate(input Registration) error {
	if len(input.ID) < 16 || len(input.ID) > 128 {
		return errors.New("invalid registration ID")
	}
	if !labelPattern.MatchString(input.Project) || !labelPattern.MatchString(input.Worktree) || len(input.Project)+len(input.Worktree)+1 > 63 {
		return errors.New("project and worktree must form a valid hostname label")
	}
	if strings.TrimSpace(input.Service) == "" || len(input.Service) > 80 {
		return errors.New("invalid service name")
	}
	if !routePattern.MatchString(input.Route) {
		return errors.New("route must be / or a path prefix such as /api")
	}
	if input.Port < 1 || input.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	return nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	defer r.Body.Close()
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "request must contain one JSON value", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
