package main

import (
	"errors"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "devproxy",
		Short:         "Local HTTPS names for services in Git worktrees",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newServeCommand(), newRunCommand(), newRegisterCommand(), newListCommand(), newInspectCommand())
	return root
}

func newServeCommand() *cobra.Command {
	var listen, admin string
	var dockerDesktop bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the local proxy, dashboard, and control API",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return errors.New("serve does not accept positional arguments")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error { return serve(listen, admin, dockerDesktop) },
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:7654", "local control and dashboard address")
	cmd.Flags().StringVar(&admin, "caddy-admin", "", "use an external Caddy admin API instead of embedded Caddy")
	cmd.Flags().BoolVar(&dockerDesktop, "docker-desktop", false, "connect to external Caddy in the Docker Desktop bridge setup")
	return cmd
}

type serviceFlags struct {
	project  *string
	worktree *string
	route    *string
	daemon   *string
	port     *int
}

func addServiceFlags(fs *pflag.FlagSet, withPort bool) serviceFlags {
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

func newRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [flags] SERVICE -- COMMAND [ARGS...]",
		Short: "Run and register a service in this worktree",
		Long:  "Run derives the worktree name from the current Git checkout, sets PORT and DEVPROXY_URL, and replaces {port} in child command arguments.",
	}
	f := addServiceFlags(cmd.Flags(), false)
	readyTimeout := cmd.Flags().Duration("ready-timeout", 60*time.Second, "time allowed for the service to start listening")
	urlEnv := cmd.Flags().String("url-env", "", "also set this environment variable to the public URL")
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if cmd.ArgsLenAtDash() != 1 || len(args) < 2 {
			return errors.New("expected SERVICE -- COMMAND [ARGS...]")
		}
		return nil
	}
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		return runService(f, *readyTimeout, *urlEnv, args[0], args[1:])
	}
	return cmd
}

func newRegisterCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register [flags] SERVICE",
		Short: "Register an already running local service",
		Long:  "Register derives the worktree name from the current Git checkout and renews the registration until stopped.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("expected --port PORT SERVICE")
			}
			return nil
		},
	}
	f := addServiceFlags(cmd.Flags(), true)
	cmd.RunE = func(_ *cobra.Command, args []string) error { return registerService(f, args[0]) }
	return cmd
}

func newListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List active registrations",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return errors.New("list does not accept positional arguments")
			}
			return nil
		},
	}
	daemon := cmd.Flags().String("daemon", defaultDaemon, "devproxy daemon origin")
	asJSON := cmd.Flags().Bool("json", false, "emit machine-readable JSON")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return listServices(*daemon, *asJSON, cmd.OutOrStdout())
	}
	return cmd
}

func newInspectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect [flags] ID|HOST|URL",
		Short: "Inspect one active registration",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("expected one registration ID, hostname, or service URL")
			}
			return nil
		},
	}
	daemon := cmd.Flags().String("daemon", defaultDaemon, "devproxy daemon origin")
	asJSON := cmd.Flags().Bool("json", false, "emit machine-readable JSON")
	route := cmd.Flags().String("route", "", "select a route when multiple services share a host")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return inspectService(*daemon, *asJSON, *route, args[0], cmd.OutOrStdout())
	}
	return cmd
}
