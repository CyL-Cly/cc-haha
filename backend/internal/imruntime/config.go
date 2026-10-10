// Package imruntime ports shared IM configuration, file bindings and server
// transport. Platform handlers use these boundaries instead of a second agent.
package imruntime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Config struct {
	Home, ConfigDir, WorkDir, ServerURL, Token string
	Roots                                      []string
	Document                                   protocol.JSONObject
	Environment                                map[string]string
	Err                                        error
}

func raw(v any) json.RawMessage { value, _ := json.Marshal(v); return value }
func object(value json.RawMessage) protocol.JSONObject {
	var result protocol.JSONObject
	_ = result.UnmarshalJSON(value)
	return result
}
func stringField(o protocol.JSONObject, key string) string { return o.String(key) }
func truthy(value json.RawMessage) bool {
	return len(value) > 0 && string(value) != "null" && string(value) != "false" && string(value) != "0" && string(value) != `""`
}
func stringsField(value json.RawMessage) []string {
	var values []json.RawMessage
	_ = json.Unmarshal(value, &values)
	result := []string{}
	for _, value := range values {
		var text string
		if len(value) > 0 && value[0] == '"' && json.Unmarshal(value, &text) == nil {
			if text = protocol.TrimJavaScriptString(text); text != "" {
				result = append(result, text)
			}
		}
	}
	return result
}
func existingDir(path, home string) string {
	path = protocol.TrimJavaScriptString(path)
	if path == "~" {
		path = home
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return ""
	}
	return resolved
}
func within(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || !strings.HasPrefix(relative, "..") && !filepath.IsAbs(relative))
}
func usableBoundary(path, home string) string {
	if path == "" || filepath.Dir(path) == path || path != home && within(home, path) {
		return ""
	}
	return path
}
func LoadConfig(home, cwd string, env map[string]string, warn func(string)) Config {
	return LoadPlatformConfig("telegram", home, cwd, env, warn)
}

// LoadPlatformConfig selects the original platform workspace and credentials;
// LoadConfig preserves the Telegram call sites already using that boundary.
func LoadPlatformConfig(platform, home, cwd string, env map[string]string, warn func(string)) Config {
	config := env["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = filepath.Join(home, ".claude")
	}
	if !filepath.IsAbs(config) {
		config = filepath.Join(cwd, config)
	}
	document := protocol.NewJSONObject()
	var loadError error
	data, err := os.ReadFile(filepath.Join(config, "adapters.json"))
	if err == nil {
		decoded := protocol.DecodeJavaScriptUTF8(data)
		if !json.Valid(decoded) {
			document = protocol.NewJSONObject()
			warn("[Config] Failed to parse " + filepath.Join(config, "adapters.json") + ", using defaults")
		} else {
			trimmed := strings.TrimSpace(string(decoded))
			if trimmed == "null" {
				loadError = errors.New("adapters.json null does not support property access")
			} else if len(trimmed) > 0 && trimmed[0] == '{' {
				_ = document.UnmarshalJSON(decoded)
			}
		}
	} else if !os.IsNotExist(err) {
		warn("[Config] Failed to parse " + filepath.Join(config, "adapters.json") + ", using defaults")
	}
	fallback := ""
	for _, candidate := range []string{env["ADAPTER_DEFAULT_PROJECT_DIR"], env["CLAUDE_ADAPTER_DEFAULT_WORK_DIR"], env["PWD"], cwd} {
		if resolved := usableBoundary(existingDir(candidate, home), home); resolved != "" {
			fallback = resolved
			break
		}
	}
	if fallback == "" {
		fallback = existingDir(home, home)
		if fallback == "" {
			fallback = home
		}
	}
	tg := object(document.Get(platform))
	for _, field := range []json.RawMessage{document.Get("defaultProjectDir"), tg.Get("defaultWorkDir"), document.Get("serverUrl")} {
		if truthy(field) && len(field) > 0 && field[0] != '"' {
			loadError = errors.New("adapter workspace/server URL requires string operations")
		}
	}
	configured := document.String("defaultProjectDir")
	if configured == "" {
		configured = tg.String("defaultWorkDir")
	}
	if configured == "" {
		configured = fallback
	}
	roots := stringsField(tg.Get("allowedProjectRoots"))
	if len(roots) == 0 {
		roots = stringsField(document.Get("allowedProjectRoots"))
	}
	if configuredEnv := protocol.TrimJavaScriptString(env["ADAPTER_ALLOWED_PROJECT_ROOTS"]); configuredEnv != "" {
		values := []string{}
		for _, value := range strings.Split(configuredEnv, string(os.PathListSeparator)) {
			if value = protocol.TrimJavaScriptString(value); value != "" {
				values = append(values, value)
			}
		}
		if len(values) > 0 {
			roots = values
		}
	}
	resolvedRoots := []string{}
	seen := map[string]bool{}
	missing := 0
	for _, root := range roots {
		value := existingDir(root, home)
		if value == "" {
			missing++
			continue
		}
		if !seen[value] {
			resolvedRoots = append(resolvedRoots, value)
			seen[value] = true
		}
	}
	if len(resolvedRoots) > 0 {
		if missing > 0 {
			message := "[Config] Ignoring 1 allowedProjectRoots entry that does not exist"
			if missing > 1 {
				message = "[Config] Ignoring " + stringNumber(missing) + " allowedProjectRoots entries that do not exist"
			}
			warn(message)
		}
	} else {
		if len(roots) > 0 {
			warn("[Config] None of the configured allowedProjectRoots exist; falling back to the default roots (home directory + default project dir)")
		}
		for _, root := range []string{existingDir(home, home), usableBoundary(existingDir(configured, home), home)} {
			if root != "" && !seen[root] {
				resolvedRoots = append(resolvedRoots, root)
				seen[root] = true
			}
		}
		if len(resolvedRoots) == 0 {
			resolvedRoots = []string{home}
		}
	}
	work := existingDir(configured, home)
	allowed := false
	for _, root := range resolvedRoots {
		allowed = allowed || within(work, root)
	}
	if work == "" || !allowed {
		if work != "" {
			warn("[Config] Default project " + work + " is outside the allowed project roots; new sessions will start in " + resolvedRoots[0])
		}
		work = resolvedRoots[0]
	}
	server := env["ADAPTER_SERVER_URL"]
	if server == "" {
		server = document.String("serverUrl")
	}
	if server == "" {
		server = "ws://127.0.0.1:3456"
	}
	token := env[strings.ToUpper(platform)+"_BOT_TOKEN"]
	if token == "" {
		if field := tg.Get("botToken"); truthy(field) {
			text, _ := protocol.JavaScriptStringJSON(field)
			token = TextJSON(text).String()
		}
	}
	return Config{Home: home, ConfigDir: config, WorkDir: work, ServerURL: server, Token: token, Roots: resolvedRoots, Document: document, Environment: env, Err: loadError}
}
func stringNumber(n int) string { return strconv.Itoa(n) }
