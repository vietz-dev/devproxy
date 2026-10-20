package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type cliError struct {
	code    int
	name    string
	message string
	run     string
}

func (e *cliError) Error() string { return e.message }
func (e *cliError) ExitCode() int { return e.code }

func usage(message, run string) error {
	return &cliError{code: 2, name: "INVALID_USAGE", message: message, run: run}
}

func notFound(message, run string) error {
	return &cliError{code: 4, name: "NOT_FOUND", message: message, run: run}
}

func conflict(message, run string) error {
	return &cliError{code: 5, name: "CONFLICT", message: message, run: run}
}

func renderCLIError(out io.Writer, err error, asJSON bool) {
	var typed *cliError
	if !errors.As(err, &typed) {
		typed = &cliError{code: 1, name: "RUNTIME_ERROR", message: err.Error(), run: "devproxy --help"}
		if strings.HasPrefix(err.Error(), "unknown command ") {
			typed.code, typed.name = 2, "INVALID_USAGE"
		}
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) {
			typed.code, typed.name = exit.ExitCode(), "CHILD_EXIT"
		}
	}
	if asJSON {
		_ = json.NewEncoder(out).Encode(map[string]any{"error": map[string]any{
			"code": typed.name, "exit_code": typed.code, "message": typed.message,
			"run": typed.run, "retryable": false,
		}})
		return
	}
	fmt.Fprintln(out, "devproxy:", typed.message)
	if typed.run != "" {
		fmt.Fprintln(out, "Run:", typed.run)
	}
}

func wantsJSON(args []string) bool {
	wanted, _ := jsonArgument(args)
	return wanted
}

func jsonArgument(args []string) (bool, bool) {
	wanted := false
	found := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--json" {
			wanted = true
			found = true
		} else if strings.HasPrefix(arg, "--json=") {
			if value, err := strconv.ParseBool(strings.TrimPrefix(arg, "--json=")); err == nil {
				wanted, found = value, true
			}
		}
	}
	return wanted, found
}

func configArgument(args []string) (string, bool) {
	var path string
	found := false
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if args[i] == "--config" && i+1 < len(args) && args[i+1] != "--" {
			path, found = args[i+1], true
			i++
		} else if strings.HasPrefix(args[i], "--config=") {
			path, found = strings.TrimPrefix(args[i], "--config="), true
		}
	}
	return path, found
}

func errorAsJSON(config *cliConfig, args []string) bool {
	if config.loaded {
		return config.getBool("json")
	}
	if wanted, found := jsonArgument(args); found {
		return wanted
	}
	if value := os.Getenv("DEVPROXY_JSON"); value != "" {
		if enabled, err := strconv.ParseBool(value); err == nil {
			return enabled
		}
	}
	return config.v.GetBool("json")
}

func errorAsJSONAfterExecution(root *cobra.Command, config *cliConfig, args []string) bool {
	loaded := config.loaded
	if !loaded {
		if path, found := configArgument(args); found {
			config.file = path
		}
		_ = config.load(root)
		if wanted, found := jsonArgument(args); found {
			return wanted
		}
	}
	return errorAsJSON(config, args)
}
