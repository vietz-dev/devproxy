package devproxy

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
)

func loadEmbeddedCaddy(config, storageDir string) error {
	config = strings.Replace(config, "{\n", "{\n\tpersist_config off\n\tstorage file_system "+strconv.Quote(storageDir)+"\n", 1)
	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		return fmt.Errorf("Caddyfile adapter is unavailable")
	}
	jsonConfig, _, err := adapter.Adapt([]byte(config), nil)
	if err != nil {
		return fmt.Errorf("adapt Caddyfile: %w", err)
	}
	if err := caddy.Load(jsonConfig, false); err != nil {
		return fmt.Errorf("load Caddy config: %w", err)
	}
	return nil
}

func stopEmbeddedCaddy() error {
	return caddy.Stop()
}
