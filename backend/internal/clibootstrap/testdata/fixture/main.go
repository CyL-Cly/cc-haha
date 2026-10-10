package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
)

func main() {
	var input struct {
		Args []string `json:"args"`
		Home string   `json:"home"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		panic(err)
	}
	if input.Home == "" || input.Home != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") {
		panic("bootstrap fixture requires disposable HOME/config")
	}
	result := clibootstrap.Prepare(input.Args, clibootstrap.Options{WorkingDir: input.Home, TempDir: filepath.Join(input.Home, "tmp")})
	var marker any
	if value, found := os.LookupEnv("CC_HAHA_CLI_BOOTSTRAP_MARKER"); found {
		marker = value
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		clibootstrap.Result
		Environment map[string]any `json:"environment"`
	}{result, map[string]any{"marker": marker}}); err != nil {
		panic(err)
	}
}
