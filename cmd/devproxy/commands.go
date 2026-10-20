package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/afero"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

func newRootCommand() *cobra.Command {
	root, _ := newRootCommandWithConfig(afero.NewOsFs())
	return root
}

func newRootCommandWithFS(fs afero.Fs) *cobra.Command {
	root, _ := newRootCommandWithConfig(fs)
	return root
}

func newRootCommandWithConfig(fs afero.Fs) (*cobra.Command, *cliConfig) {
	config := newCLIConfig(fs)
	root := &cobra.Command{
		Use:           "devproxy",
		Short:         "Local HTTPS names for services in Git worktrees",
		Example:       "  devproxy start\n  devproxy run --project example web -- npm run dev\n  devproxy list --json",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.PersistentFlags().StringVar(&config.file, "config", "", "read an additional YAML config file")
	root.PersistentFlags().String("daemon", defaultDaemon, "devproxy daemon origin")
	root.PersistentFlags().Bool("json", false, "emit machine-readable JSON")
	root.PersistentFlags().Bool("plain", false, "emit tab-separated records without headers")
	root.PersistentFlags().BoolP("quiet", "q", false, "suppress non-essential status messages")
	root.PersistentFlags().Bool("no-color", false, "disable ANSI color")
	root.PersistentFlags().Bool("no-input", false, "never prompt for input")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd == root {
			return nil
		}
		return config.load(cmd)
	}
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usage(err.Error(), cmd.CommandPath()+" --help")
	})
	root.AddCommand(newServeCommand(config), newStartCommand(config), newStopCommand(config), newStatusCommand(config), newRunCommand(config), newRegisterCommand(config), newListCommand(config), newInspectCommand(config))
	root.AddCommand(newServicesCommand(config))
	root.AddCommand(newVersionCommand(config))
	root.AddCommand(newConfigCommand(config))
	return root, config
}

func newServicesCommand(config *cliConfig) *cobra.Command {
	group := &cobra.Command{
		Use:     "services",
		Short:   "Run and inspect local services",
		Example: "  devproxy services list\n  devproxy services describe example-feature.localhost\n  devproxy services run --project example web -- npm run dev",
	}
	for _, child := range []*cobra.Command{newRunCommand(config), newRegisterCommand(config), newListCommand(config), newInspectCommand(config)} {
		child.Example = strings.ReplaceAll(child.Example, "devproxy "+child.Name(), "devproxy services "+child.Name())
		if child.Name() == "inspect" {
			child.Use = "describe [flags] ID|HOST|URL"
			child.Aliases = []string{"inspect"}
			child.Example = strings.ReplaceAll(child.Example, "services inspect", "services describe")
		}
		group.AddCommand(child)
	}
	return group
}

func newServeCommand(config *cliConfig) *cobra.Command {
	var listen, admin string
	var dockerDesktop bool
	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "Serve the local proxy, dashboard, and control API",
		Example: "  devproxy serve\n  devproxy serve --listen 127.0.0.1:7655\n  devproxy serve --caddy-admin http://127.0.0.1:2019",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return usage("serve does not accept positional arguments", "devproxy serve --help")
			}
			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return serve(config.getString("listen"), config.getString("caddy-admin"), config.getBool("docker-desktop"))
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:7654", "local control and dashboard address")
	cmd.Flags().StringVar(&admin, "caddy-admin", "", "use an external Caddy admin API instead of embedded Caddy")
	cmd.Flags().BoolVar(&dockerDesktop, "docker-desktop", false, "connect to external Caddy in the Docker Desktop bridge setup")
	return cmd
}

func newStartCommand(config *cliConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use: "start", Short: "Start devproxy in the background",
		Example: "  devproxy start\n  devproxy start --caddy-admin http://127.0.0.1:2019",
		Args:    lifecycleNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return startBackground(config.getString("listen"), config.getString("caddy-admin"), config.getBool("docker-desktop"), statusWriter(cmd, config))
		},
	}
	cmd.Flags().String("listen", "127.0.0.1:7654", "local control and dashboard address")
	cmd.Flags().String("caddy-admin", "", "use an external Caddy admin API instead of embedded Caddy")
	cmd.Flags().Bool("docker-desktop", false, "connect to external Caddy in the Docker Desktop bridge setup")
	return cmd
}

func newStopCommand(config *cliConfig) *cobra.Command {
	return &cobra.Command{
		Use: "stop", Short: "Stop the local devproxy daemon",
		Args: lifecycleNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return stopBackground(config.getString("daemon"), statusWriter(cmd, config))
		},
	}
}

func statusWriter(cmd *cobra.Command, config *cliConfig) io.Writer {
	if config.getBool("quiet") {
		return io.Discard
	}
	return cmd.ErrOrStderr()
}

func newStatusCommand(config *cliConfig) *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Show whether the local devproxy daemon is running",
		Args: lifecycleNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return statusBackground(config.getString("daemon"), config.getBool("json"), cmd.OutOrStdout())
		},
	}
}

func lifecycleNoArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 0 {
		return usage(cmd.Name()+" does not accept positional arguments", cmd.CommandPath()+" --help")
	}
	return nil
}

type serviceFlags struct {
	project  *string
	worktree *string
	route    *string
	daemon   *string
	port     *int
	quiet    bool
}

func (f *serviceFlags) apply(config *cliConfig) {
	*f.project = config.getString("project")
	*f.worktree = config.getString("worktree")
	*f.route = config.getString("route")
	*f.daemon = config.getString("daemon")
	f.quiet = config.getBool("quiet")
	if f.port != nil {
		*f.port = config.getInt("port")
	}
}

func addServiceFlags(fs *pflag.FlagSet, withPort bool) serviceFlags {
	f := serviceFlags{
		project:  fs.String("project", "", "stable project name"),
		worktree: fs.String("worktree", "", "worktree name (defaults to git root folder)"),
		route:    fs.String("route", "/", "HTTP path prefix"),
		daemon:   new(string),
	}
	if withPort {
		f.port = fs.Int("port", 0, "already-running service port")
	}
	return f
}

func newRunCommand(config *cliConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "run [flags] SERVICE -- COMMAND [ARGS...]",
		Short:   "Run and register a service in this worktree",
		Long:    "Run derives the worktree name from the current Git checkout, sets PORT and DEVPROXY_URL, and replaces {port} in child command arguments.",
		Example: "  devproxy run --project example web -- npm run dev\n  devproxy run --project example --route /api api -- server --port '{port}'\n  devproxy run --project example --ready-timeout 30s web -- npm run dev",
	}
	f := addServiceFlags(cmd.Flags(), false)
	cmd.Flags().Duration("ready-timeout", 60*time.Second, "time allowed for the service to start listening")
	cmd.Flags().String("url-env", "", "also set this environment variable to the public URL")
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if cmd.ArgsLenAtDash() != 1 || len(args) < 2 {
			return usage("expected SERVICE -- COMMAND [ARGS...]", "devproxy run --help")
		}
		return nil
	}
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		f.apply(config)
		return runService(f, config.v.GetDuration("ready-timeout"), config.getString("url-env"), args[0], args[1:])
	}
	return cmd
}

func newRegisterCommand(config *cliConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "register [flags] SERVICE",
		Short:   "Register an already running local service",
		Long:    "Register derives the worktree name from the current Git checkout and renews the registration until stopped.",
		Example: "  devproxy register --project example --port 3000 web\n  devproxy register --project example --route /api --port 3001 api",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("expected --port PORT SERVICE", "devproxy register --help")
			}
			return nil
		},
	}
	f := addServiceFlags(cmd.Flags(), true)
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		f.apply(config)
		return registerService(f, args[0])
	}
	return cmd
}

func newListCommand(config *cliConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List active registrations",
		Example: "  devproxy list\n  devproxy list --json\n  devproxy list --plain | cut -f1",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return usage("list does not accept positional arguments", "devproxy list --help")
			}
			return nil
		},
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		plain := config.getBool("plain")
		if file, ok := cmd.OutOrStdout().(*os.File); !ok || !term.IsTerminal(int(file.Fd())) {
			plain = true
		}
		return listServicesFormatted(config.getString("daemon"), config.getBool("json"), plain, cmd.OutOrStdout())
	}
	return cmd
}

func newInspectCommand(config *cliConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "inspect [flags] ID|HOST|URL",
		Short:   "Inspect one active registration",
		Example: "  devproxy inspect example-feature.localhost\n  devproxy inspect example-feature.localhost --route /api\n  devproxy inspect example-feature.localhost --json",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("expected one registration ID, hostname, or service URL", "devproxy inspect --help")
			}
			return nil
		},
	}
	cmd.Flags().String("route", "", "select a route when multiple services share a host")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return inspectService(config.getString("daemon"), config.getBool("json"), config.getString("route"), args[0], cmd.OutOrStdout())
	}
	return cmd
}

func newVersionCommand(config *cliConfig) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Show the devproxy version",
		Example: "  devproxy version\n  devproxy version --json",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return usage("version does not accept positional arguments", "devproxy version --help")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if config.getBool("json") {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), `{"version":"dev"}`)
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "devproxy dev")
			return err
		},
	}
}

func newConfigCommand(config *cliConfig) *cobra.Command {
	group := &cobra.Command{
		Use:     "config",
		Short:   "Inspect effective CLI configuration",
		Example: "  devproxy config get daemon\n  devproxy config get daemon --origin\n  devproxy config get route --json",
	}
	get := &cobra.Command{
		Use:     "get KEY",
		Short:   "Get a configuration setting",
		Example: "  devproxy config get daemon\n  devproxy config get daemon --origin\n  devproxy config get route --json",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usage("expected one configuration key", "devproxy config get daemon")
			}
			return nil
		},
	}
	showOrigin := get.Flags().Bool("origin", false, "show where the effective value came from")
	get.RunE = func(cmd *cobra.Command, args []string) error {
		key := args[0]
		defaultValue, known := configDefaults[key]
		if !known {
			return usage(fmt.Sprintf("unknown configuration key %q", key), "devproxy config get daemon")
		}
		var value any
		switch defaultValue.(type) {
		case bool:
			value = config.getBool(key)
		case int:
			value = config.getInt(key)
		default:
			value = config.getString(key)
		}
		if !config.v.IsSet(key) {
			value = defaultValue
		}
		origin := config.source(cmd, key)
		if config.getBool("json") {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"key": key, "value": value, "origin": origin})
		}
		if *showOrigin {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%v\t%s\n", value, origin)
			return err
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), value)
		return err
	}
	group.AddCommand(get)
	return group
}
