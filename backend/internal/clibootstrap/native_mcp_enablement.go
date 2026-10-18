package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func originalGlobalConfigDirectory(n *NativeBoundary, config string) *string {
	if n.NativeProcess {
		directory, present := n.nativeEnvironmentValue("CLAUDE_CONFIG_DIR")
		if !present {
			return nil
		}
		return &directory
	}
	return &config
}

// MCPExecutionEnabled reads the original enablement record uncached before
// every peer admission. A second process's disable applies to cached peers.
func (p *NativeProcessSettings) MCPExecutionEnabled(ctx context.Context, name string, _ protocol.JSONObject) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false, mainCap("closed server settings context")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	path := p.global.Path()
	data, err := p.n.readFile(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	failure := func() (bool, error) { return false, fmt.Errorf("Cannot read MCP enablement state for \"%s\"", name) }
	if err != nil {
		return failure()
	}
	var config protocol.JSONObject
	if config.UnmarshalJSON(bytes.TrimPrefix(protocol.DecodeJavaScriptUTF8(data), []byte{0xef, 0xbb, 0xbf})) != nil || !config.IsObject() {
		return failure()
	}
	projects := protocol.NewJSONObject()
	if value := config.Get("projects"); value != nil {
		if projects.UnmarshalJSON(value) != nil || !projects.IsObject() {
			return failure()
		}
	}
	key := usercontext.CanonicalGitRoot(mainSettingsFiles{n: p.n}, func(path string) (string, error) { return filepath.EvalSymlinks(path) }, p.n.CWD)
	if key == "" {
		key = p.n.CWD
	}
	key = strings.ReplaceAll(key, "\\", "/")
	project := protocol.NewJSONObject()
	if value := projects.Get(key); value != nil {
		if project.UnmarshalJSON(value) != nil || !project.IsObject() {
			return failure()
		}
	}
	if value := project.Get("disabledMcpServers"); value != nil {
		var disabled []json.RawMessage
		if len(bytes.TrimSpace(value)) == 0 || bytes.TrimSpace(value)[0] != '[' || json.Unmarshal(value, &disabled) != nil {
			return failure()
		}
		found := false
		for _, item := range disabled {
			var server string
			if json.Unmarshal(item, &server) != nil || len(bytes.TrimSpace(item)) == 0 || bytes.TrimSpace(item)[0] != '"' {
				return failure()
			}
			if server == name {
				found = true
			}
		}
		return !found, nil
	}
	// Source Computer Use defaults enabled here; its global consent remains a
	// separate runtime boundary and is never inferred from this membership.
	return true, nil
}
