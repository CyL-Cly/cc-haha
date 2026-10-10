package mcpcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// DesktopFiles uses the original platform and physical filesystem boundary.
// WSL discovery must not reinterpret a Windows drive as a POSIX user profile.
type DesktopFiles struct {
	Platform, Home, UserProfile string
	Stat                        func(string) (fs.FileInfo, error)
	ReadDir                     func(string) ([]fs.DirEntry, error)
	ReadFile                    func(string) ([]byte, error)
}

func DesktopConfigPath(o DesktopFiles) (string, error) {
	if o.Platform != "macos" && o.Platform != "wsl" {
		return "", fmt.Errorf("Unsupported platform: %s - Claude Desktop integration only works on macOS and WSL.", o.Platform)
	}
	if o.Platform == "macos" {
		return filepath.Join(o.Home, "Library", "Application Support", "Claude", "claude_desktop_config.json"), nil
	}
	if o.UserProfile != "" {
		p := strings.ReplaceAll(o.UserProfile, "\\", "/")
		if len(p) > 1 && p[0] >= 'A' && p[0] <= 'Z' && p[1] == ':' {
			p = p[2:]
		}
		p = "/mnt/c" + p + "/AppData/Roaming/Claude/claude_desktop_config.json"
		if _, err := o.Stat(p); err == nil {
			return p, nil
		}
	}
	if entries, err := o.ReadDir("/mnt/c/Users"); err == nil {
		for _, entry := range entries {
			switch entry.Name() {
			case "Public", "Default", "Default User", "All Users":
				continue
			}
			p := filepath.Join("/mnt/c/Users", entry.Name(), "AppData", "Roaming", "Claude", "claude_desktop_config.json")
			if _, err := o.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", errors.New("Could not find Claude Desktop config file in Windows. Make sure Claude Desktop is installed on Windows.")
}

func ReadDesktopServers(o DesktopFiles) (protocol.JSONObject, error) {
	if o.Platform != "macos" && o.Platform != "wsl" {
		return protocol.JSONObject{}, errors.New("Unsupported platform - Claude Desktop integration only works on macOS and WSL.")
	}
	if o.Stat == nil || o.ReadDir == nil || o.ReadFile == nil {
		return protocol.JSONObject{}, errors.New("Claude Desktop import requires physical filesystem producers")
	}
	result := protocol.NewJSONObject()
	p, err := DesktopConfigPath(o)
	if err != nil {
		return result, nil
	} // Source reader catches discovery/IO failures.
	raw, err := o.ReadFile(p)
	if err != nil {
		return result, nil
	}
	value := object(protocol.DecodeJavaScriptUTF8(raw))
	serverRaw := value.Get("mcpServers")
	fields := object(serverRaw).OwnProperties()
	var array []json.RawMessage
	if json.Unmarshal(serverRaw, &array) == nil && strings.HasPrefix(string(serverRaw), "[") {
		for i, raw := range array {
			fields = append(fields, protocol.JSONProperty{NameJSON: encode(strconv.Itoa(i)), Value: raw})
		}
	}
	for _, field := range fields {
		entry := object(field.Value)
		if kind := entry.Get("type"); kind != nil && text(kind) != "stdio" {
			continue
		}
		parsed, issues := mcpconfig.ValidateServer(field.Value)
		if len(issues) == 0 && parsed.IsObject() {
			// The original ordinary object ignores the prototype setter key.
			if text(field.NameJSON) != "__proto__" {
				_ = result.SetPropertyJSON(field.NameJSON, encode(parsed))
			}
		}
	}
	return result, nil
}

type DesktopSelection struct {
	Names    []string
	Existing protocol.JSONObject
	Selected []string
}

func NewDesktopSelection(servers, existing protocol.JSONObject) DesktopSelection {
	s := DesktopSelection{Names: []string{}, Existing: existing.Clone(), Selected: []string{}}
	for _, field := range servers.OwnProperties() {
		name := text(field.NameJSON)
		s.Names = append(s.Names, name)
		if existing.Get(name) == nil {
			s.Selected = append(s.Selected, name)
		}
	}
	return s
}

// ImportDesktopSelection preserves Source collision naming and sequential
// mutation. Existing is the single snapshot used by the original dialog.
func ImportDesktopSelection(ctx context.Context, servers protocol.JSONObject, selection DesktopSelection, scope string, add func(context.Context, string, string, []byte) error) (int, error) {
	count := 0
	for _, name := range selection.Selected {
		config := servers.Get(name)
		if config == nil {
			continue
		}
		final := name
		if selection.Existing.Get(final) != nil {
			for counter := 1; ; counter++ {
				candidate := fmt.Sprintf("%s_%d", name, counter)
				if selection.Existing.Get(candidate) == nil {
					final = candidate
					break
				}
			}
		}
		if err := add(ctx, final, scope, config); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
