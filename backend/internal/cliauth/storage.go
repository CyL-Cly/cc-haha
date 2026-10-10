package cliauth

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// StorageOptions makes the external secure-storage capability explicit. A
// native keychain adapter is not implemented by this read-only component.
type StorageOptions struct {
	Home        string
	Environment func(string) string
	ReadFile    func(string) ([]byte, error)
	Exists      func(string) bool
	ReadSecure  func() (json.RawMessage, error)
	Observe     func(operation, path string)
}

// OpenAIStorage retains the original per-path/file vs secure-storage cache.
// Missing/invalid files and failed secure reads are cached as null as well.
type OpenAIStorage struct {
	mu      sync.Mutex
	options StorageOptions
	cache   map[string]json.RawMessage
}

func NewOpenAIStorage(options StorageOptions) *OpenAIStorage {
	if options.Environment == nil {
		options.Environment = func(string) string { return "" }
	}
	if options.ReadFile == nil {
		options.ReadFile = os.ReadFile
	}
	if options.Exists == nil {
		options.Exists = func(path string) bool { _, err := os.Stat(path); return err == nil }
	}
	return &OpenAIStorage{options: options, cache: map[string]json.RawMessage{}}
}

func (storage *OpenAIStorage) observe(operation, path string) {
	if storage.options.Observe != nil {
		storage.options.Observe(operation, path)
	}
}

func trimEnvironment(value string) string {
	// String.trim uses ECMAScript whitespace, excluding U+0085.
	return strings.TrimFunc(value, func(r rune) bool {
		return r >= '\t' && r <= '\r' || r == ' ' || r == '\u00a0' || r == '\u1680' || r >= '\u2000' && r <= '\u200a' || r == '\u2028' || r == '\u2029' || r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\ufeff'
	})
}

func rawField(raw json.RawMessage, name string) json.RawMessage {
	var object protocol.JSONObject
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil
	}
	return object.Get(name)
}

func isString(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '"'
}

func isNumber(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && (trimmed[0] == '-' || trimmed[0] >= '0' && trimmed[0] <= '9')
}

func normalizeDesktopFile(data []byte) json.RawMessage {
	// Node UTF-8 replacement followed by JSON.parse, without stripping a BOM.
	raw := protocol.DecodeJavaScriptUTF8(data)
	if !json.Valid(raw) || !isString(rawField(raw, "accessToken")) || !isString(rawField(raw, "refreshToken")) || !isNumber(rawField(raw, "expiresAt")) {
		return nil
	}
	object := protocol.NewJSONObject()
	for _, name := range []string{"accessToken", "refreshToken", "expiresAt", "idToken", "accountId", "email", "clientId"} {
		field := rawField(raw, name)
		if name == "expiresAt" || isString(field) {
			object.Set(name, field)
		}
	}
	result, _ := object.MarshalJSON() // All fields are from the validated input.
	return result
}

func (storage *OpenAIStorage) read(path string) json.RawMessage {
	if path != "" {
		storage.observe("file", path)
		data, err := storage.options.ReadFile(path)
		if err != nil {
			return nil
		}
		return normalizeDesktopFile(data)
	}
	config := storage.options.Environment("CLAUDE_CONFIG_DIR")
	if config == "" {
		config = filepath.Join(storage.options.Home, ".claude")
	}
	marker := filepath.Join(config, "cc-haha", "openai-oauth-file-backed")
	storage.observe("marker", marker)
	if storage.options.Exists(marker) {
		return nil
	}
	storage.observe("secure", "")
	if storage.options.ReadSecure == nil {
		return nil
	}
	data, err := storage.options.ReadSecure()
	if err != nil {
		return nil
	}
	return rawField(data, "openaiCodexOauth")
}

func (storage *OpenAIStorage) Tokens() json.RawMessage {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	path := trimEnvironment(storage.options.Environment("OPENAI_CODEX_OAUTH_FILE"))
	key := "secure-storage"
	if path != "" {
		key = "file:" + path
	}
	raw, exists := storage.cache[key]
	if !exists {
		raw = storage.read(path)
		storage.cache[key] = append(json.RawMessage(nil), raw...)
	}
	return append(json.RawMessage(nil), raw...)
}

func (storage *OpenAIStorage) Clear() {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	storage.cache = map[string]json.RawMessage{}
}
