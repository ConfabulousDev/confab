// Package loginit wires the logger up to user configuration at process
// startup. It depends on both pkg/config and pkg/logger so neither of
// those packages has to import the other for startup orchestration.
package loginit

import (
	"github.com/ConfabulousDev/confab/pkg/config"
	"github.com/ConfabulousDev/confab/pkg/logger"
)

// ApplyLogLevel reads the log level from upload config and applies it
// to the logger. No-ops if the config can't be read; logs a warning and
// leaves the default level in place if log_level is unrecognized.
func ApplyLogLevel() {
	cfg, err := config.GetUploadConfig()
	if err != nil {
		return
	}

	level, err := config.ParseLogLevel(cfg.LogLevel)
	if err != nil {
		logger.Warn("Invalid log_level in config: %v", err)
		return
	}

	logger.Get().SetLevel(level)
}
