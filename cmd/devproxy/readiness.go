package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/vietz-dev/devproxy/internal/devproxy"
)

// serviceReadyV1 is a CLI event, not the daemon's registration wire format.
// Within version 1, fields may be added but not removed, renamed, or retyped.
type serviceReadyV1 struct {
	SchemaVersion int    `json:"schema_version"`
	Event         string `json:"event"`
	ID            string `json:"id"`
	Project       string `json:"project"`
	Worktree      string `json:"worktree"`
	Service       string `json:"service"`
	URL           string `json:"url"`
	Route         string `json:"route"`
	Port          int    `json:"port"`
	Readiness     struct {
		Registration string `json:"registration"`
		TCP          string `json:"tcp"`
		HTTPHealth   string `json:"http_health"`
	} `json:"readiness"`
}

func writeServiceReady(f serviceFlags, registration devproxy.Registration) error {
	if f.asJSON {
		result := serviceReadyV1{
			SchemaVersion: 1, Event: "service_ready", ID: registration.ID,
			Project: registration.Project, Worktree: registration.Worktree,
			Service: registration.Service, URL: publicURL(registration),
			Route: registration.Route, Port: registration.Port,
		}
		result.Readiness.Registration = "accepted"
		result.Readiness.TCP = "listening"
		result.Readiness.HTTPHealth = "not_checked"
		out := f.out
		if out == nil {
			out = os.Stdout
		}
		// Go otherwise exits on EPIPE from stdout before the supervisor can
		// stop its child and unregister. Catch (rather than ignore) SIGPIPE
		// only during this write, without changing child signal dispositions.
		brokenPipe := make(chan os.Signal, 1)
		signal.Notify(brokenPipe, syscall.SIGPIPE)
		defer signal.Stop(brokenPipe)
		if err := json.NewEncoder(out).Encode(result); err != nil {
			return fmt.Errorf("write readiness result: %w", err)
		}
		return nil
	}
	if !f.quiet {
		out := f.errOut
		if out == nil {
			out = os.Stderr
		}
		fmt.Fprintf(out, "devproxy: %s → %s on port %d\n", registration.Service, publicURL(registration), registration.Port)
	}
	return nil
}
