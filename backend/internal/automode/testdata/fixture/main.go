package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/automode"
)

func main() {
	var input struct {
		Home      string            `json:"home"`
		Operation string            `json:"operation"`
		Snapshot  automode.Snapshot `json:"snapshot"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		panic(err)
	}
	cwd, _ := os.Getwd()
	canonicalCwd, cwdErr := filepath.EvalSymlinks(cwd)
	canonicalHome, homeErr := filepath.EvalSymlinks(input.Home)
	if !filepath.IsAbs(input.Home) || input.Home != os.Getenv("HOME") || cwdErr != nil || homeErr != nil || canonicalCwd != canonicalHome || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") {
		panic("auto-mode fixture requires disposable HOME/config/cwd")
	}
	var out bytes.Buffer
	var err error
	switch input.Operation {
	case "defaults":
		err = automode.DefaultsHandler(&out, input.Snapshot.TranscriptClassifier)
	case "config":
		err = automode.ConfigHandler(&out, input.Snapshot)
	default:
		panic("unsupported auto-mode fixture operation")
	}
	if err != nil {
		panic(err)
	}
	fmt.Print(out.String())
}
