package main

import (
	"github.com/ConfabulousDev/confab/cmd"
	"github.com/ConfabulousDev/confab/pkg/http"
)

// Set by goreleaser via ldflags
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cmd.SetVersionInfo(version, commit, date)
	http.SetUserAgent(http.BuildUserAgent(version))
	cmd.Execute()
}
