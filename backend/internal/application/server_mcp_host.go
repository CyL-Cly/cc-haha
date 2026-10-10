package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func inspectMCPHostCommand(ctx context.Context, cwd string, base map[string]string, config protocol.JSONObject, shell func(context.Context) (map[string]string, error)) error {
	if kind := config.String("type"); kind != "" && kind != "stdio" {
		return nil
	}
	command := strings.TrimSpace(config.String("command"))
	if command == "" {
		return errors.New("STDIO MCP command is empty.")
	}
	executable := func(path string) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
			return os.ErrPermission
		}
		return nil
	}
	if filepath.IsAbs(command) || strings.ContainsAny(command, "/\\") {
		path := command
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if err := executable(path); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("Host command path %q could not be found at %q. This STDIO MCP runs on the host machine, so the configured executable path must exist locally.", command, path)
			}
			return fmt.Errorf("Host command path %q exists at %q but is not executable. This STDIO MCP runs on the host machine, so the configured executable must be runnable by the local OS user.", command, path)
		}
		return nil
	}
	search := func(path string) bool {
		candidates := []string{command}
		if runtime.GOOS == "windows" && filepath.Ext(command) == "" {
			extensions := base["PATHEXT"]
			if extensions == "" {
				extensions = ".EXE;.CMD;.BAT;.COM"
			}
			for _, extension := range strings.Split(extensions, ";") {
				if extension = strings.TrimSpace(extension); extension != "" {
					candidates = append(candidates, command+strings.ToLower(extension))
				}
			}
		}
		for _, dir := range filepath.SplitList(path) {
			dir = strings.TrimSpace(dir)
			if dir == "" {
				continue
			}
			for _, candidate := range candidates {
				if executable(filepath.Join(dir, candidate)) == nil {
					return true
				}
			}
		}
		return false
	}
	var env protocol.JSONObject
	_ = env.UnmarshalJSON(config.Get("env"))
	explicit := env.Get("PATH") != nil
	path := base["PATH"]
	if explicit {
		path = env.String("PATH")
	}
	if search(path) {
		return nil
	}
	if !explicit {
		captured, err := shell(ctx)
		if err != nil {
			return err
		}
		if search(captured["PATH"]) {
			return nil
		}
	}
	name := strings.ToLower(filepath.Base(command))
	hint := "Install " + fmt.Sprintf("%q", command) + " on this machine or update PATH, then retry."
	switch name {
	case "node", "npm", "npx", "pnpm", "yarn":
		hint = "Install Node.js on this machine so " + fmt.Sprintf("%q", command) + " is available in PATH, then retry."
	case "python", "python3", "pip", "pip3":
		hint = "Install Python on this machine so " + fmt.Sprintf("%q", command) + " is available in PATH, then retry."
	case "uv":
		hint = "Install uv on this machine so \"uv\" is available in PATH, then retry."
	case "bun":
		hint = "Install Bun on this machine so \"bun\" is available in PATH, then retry."
	}
	return fmt.Errorf("Host command %q is not available in PATH. This STDIO MCP runs on the host machine. %s", command, hint)
}
