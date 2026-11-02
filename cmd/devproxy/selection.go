package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vietz-dev/devproxy/internal/devproxy"
)

// Sort by stable identity so daemon response order cannot change the suggested
// registration. Quote display fields so control characters stay on one line.
func selectionDetails(services []devproxy.Service) string {
	sort.Slice(services, func(i, j int) bool { return services[i].ID < services[j].ID })
	var details strings.Builder
	for _, service := range services {
		fmt.Fprintf(&details, "\n  ID=%q host=%q route=%q", service.ID, service.Host, service.Route)
	}
	return details.String()
}

func selectionCommand(daemon, command string, args ...string) string {
	parts := []string{"devproxy", command, "--daemon", shellArgument(daemon)}
	for _, arg := range args {
		parts = append(parts, shellArgument(arg))
	}
	return strings.Join(parts, " ")
}

func shellArgument(value string) string {
	safe := value != ""
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("_-./:@", char)) {
			safe = false
			break
		}
	}
	if safe {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
