package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// cliConfig is scoped to one command execution. In particular, tests and
// callers embedding the CLI do not share Viper's package-level state.
type cliConfig struct {
	fs     afero.Fs
	v      *viper.Viper
	file   string
	origin map[string]string
	loaded bool
}

var configDefaults = map[string]any{
	"listen": "127.0.0.1:7654", "caddy-admin": "", "docker-desktop": false,
	"project": "", "worktree": "", "route": "/", "daemon": defaultDaemon,
	"port": 0, "ready-timeout": "1m0s", "url-env": "",
	"json": false, "plain": false, "quiet": false, "no-color": false, "no-input": false,
}

func newCLIConfig(fs afero.Fs) *cliConfig {
	return &cliConfig{fs: fs, v: viper.New(), origin: make(map[string]string)}
}

func (c *cliConfig) load(cmd *cobra.Command) error {
	c.loaded = false
	c.v = viper.New()
	c.origin = make(map[string]string)
	c.v.SetEnvPrefix("DEVPROXY")
	c.v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	// Bind only settings the CLI owns. DEVPROXY_URL and
	// DEVPROXY_SIGNAL_RELAY are child-process protocol variables.
	for key := range configDefaults {
		if err := c.v.BindEnv(key); err != nil {
			return err
		}
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		var err error
		configHome, err = os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("find user config directory: %w", err)
		}
	}
	if err := c.mergeFile(filepath.Join(configHome, "devproxy", "config.yaml"), false); err != nil {
		return err
	}
	if err := c.mergeFile(".devproxy.yaml", false); err != nil {
		return err
	}
	if c.file != "" {
		if err := c.mergeFile(c.file, true); err != nil {
			return err
		}
	}
	if err := c.v.BindPFlags(cmd.InheritedFlags()); err != nil {
		return err
	}
	if err := c.v.BindPFlags(cmd.Flags()); err != nil {
		return err
	}
	c.loaded = true
	return nil
}

func (c *cliConfig) mergeFile(path string, required bool) error {
	data, err := afero.ReadFile(c.fs, path)
	if os.IsNotExist(err) && !required {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	c.v.SetConfigType("yaml")
	source := viper.New()
	source.SetConfigType("yaml")
	if err := source.ReadConfig(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := c.v.MergeConfig(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	for _, key := range source.AllKeys() {
		c.origin[key] = path
	}
	return nil
}

func (c *cliConfig) getString(key string) string { return c.v.GetString(key) }
func (c *cliConfig) getBool(key string) bool     { return c.v.GetBool(key) }
func (c *cliConfig) getInt(key string) int       { return c.v.GetInt(key) }

func (c *cliConfig) source(cmd *cobra.Command, key string) string {
	if flag := cmd.Flags().Lookup(key); flag != nil && flag.Changed {
		return "flag"
	}
	if flag := cmd.InheritedFlags().Lookup(key); flag != nil && flag.Changed {
		return "flag"
	}
	if value, ok := os.LookupEnv("DEVPROXY_" + strings.ToUpper(strings.ReplaceAll(key, "-", "_"))); ok && value != "" {
		return "environment"
	}
	if path := c.origin[key]; path != "" {
		return path
	}
	return "default"
}
