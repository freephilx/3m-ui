package main

import (
	"fmt"

	"github.com/kazeyukiro/3m-ui/backend/internal/config"
	"github.com/kazeyukiro/3m-ui/backend/internal/tui"
)

// runTUI opens the terminal control center. configPath was already resolved by
// main() (THREE_M_UI_CONFIG / --config / default location).
func runTUI(configPath string) error {
	if configPath == "" {
		configPath = "/etc/3m-ui/config.yaml"
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return tui.Run(cfg, configPath)
}
