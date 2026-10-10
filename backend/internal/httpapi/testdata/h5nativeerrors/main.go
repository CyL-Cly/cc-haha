// Test-only OS error samples. No config path is discovered from the host.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type operation struct {
	Name         string `json:"name"`
	Operation    string `json:"operation"`
	Path         string `json:"path"`
	Target       string `json:"target"`
	SourceIsFile bool   `json:"sourceIsFile"`
}

func run() error {
	var input struct {
		Home  string      `json:"home"`
		Steps []operation `json:"steps"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.Home == "" || !filepath.IsAbs(input.Home) || input.Home != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") {
		return errors.New("native OS error fixture requires disposable HOME/config")
	}
	path := func(relative string) (string, error) {
		value := filepath.Join(input.Home, relative)
		name, err := filepath.Rel(input.Home, value)
		if err != nil || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return "", errors.New("native OS error fixture escaped HOME")
		}
		return value, nil
	}
	results := []map[string]string{}
	for _, step := range input.Steps {
		value, err := path(step.Path)
		if err != nil {
			return err
		}
		var cause error
		switch step.Operation {
		case "read":
			_, cause = os.ReadFile(value)
		case "write":
			cause = os.WriteFile(value, []byte("fixture"), 0600)
		case "mkdir":
			cause = os.MkdirAll(value, 0700)
		case "rename":
			target, err := path(step.Target)
			if err != nil {
				return err
			}
			cause = os.Rename(value, target)
		default:
			return errors.New("unrecognized native OS error operation")
		}
		if cause == nil {
			return fmt.Errorf("native OS fixture unexpectedly succeeded: %s", step.Name)
		}
		results = append(results, map[string]string{"name": step.Name, "rendered": storage.JavaScriptErrorText(cause, storage.JavaScriptErrorContext{RequestedPath: value, RenameSourceIsFile: step.SourceIsFile}), "native": cause.Error()})
	}
	return json.NewEncoder(os.Stdout).Encode(results)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
