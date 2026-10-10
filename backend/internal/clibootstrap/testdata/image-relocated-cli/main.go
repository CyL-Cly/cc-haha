// This executable substitutes only managed-policy and security(1) primitives.
// It exercises the public CLI adapter and shared Harness after installation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/application"
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok {
			env[key] = value
		}
	}
	root := env["CC_HAHA_IMAGE_RELOCATION_ROOT"]
	if !filepath.IsAbs(root) {
		return errors.New("private relocation root required")
	}
	for _, key := range []string{"HOME", "USERPROFILE", "CLAUDE_CONFIG_DIR", "TMPDIR"} {
		rel, err := filepath.Rel(root, env[key])
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return errors.New("relocation fixture path escapes private root")
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: runtime.GOOS, ManagedRoot: filepath.Join(root, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		return err
	}
	native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("private relocation fixture Keychain unavailable")
	}
	// No library override or repository/CWD dependency lookup is supplied.
	library, err := imageprocessing.FindBundledLibrary()
	if err != nil {
		return err
	}
	receipt, _ := json.Marshal(map[string]string{"library": library, "cwd": cwd})
	if err = os.WriteFile(filepath.Join(root, "library-receipt.json"), receipt, 0600); err != nil {
		return err
	}
	command, parsed, err := application.ParseCommand(os.Args[1:], "fixture", cwd, os.TempDir(), env)
	if err != nil {
		return err
	}
	if parsed.Parsed == nil {
		return errors.New("relocation fixture requires an actual CLI command")
	}
	code, err := application.RunCommand(context.Background(), command, application.Options{Native: native, Environment: native.Environment, WorkDir: native.CWD, Version: "fixture", Platform: runtime.GOOS, Stderr: os.Stderr}, os.Stdin, os.Stdout, os.Environ())
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("actual CLI exited %d", code)
	}
	return nil
}
