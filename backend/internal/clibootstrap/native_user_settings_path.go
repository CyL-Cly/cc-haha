package clibootstrap

import (
	"encoding/json"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"golang.org/x/text/unicode/norm"
)

func (n *NativeBoundary) nativeUserSettingsPath(cwd string, environment *clistartupenv.Environment) (string, error) {
	value := func(key string) (string, bool) {
		if environment == nil {
			return n.nativeEnvironmentValue(key)
		}
		raw := environment.Get(key)
		if raw == nil {
			return "", false
		}
		var text string
		err := json.Unmarshal(raw, &text)
		return text, err == nil
	}
	config, exists := value("CLAUDE_CONFIG_DIR")
	if !exists {
		config = filepath.Join(n.Home, ".claude")
	}
	config = norm.NFC.String(config)
	if !filepath.IsAbs(config) {
		config = filepath.Join(cwd, config)
	}
	filename := "settings.json"
	if cowork, _ := value("CLAUDE_CODE_USE_COWORK_PLUGINS"); envTruthyNative(cowork) {
		filename = "cowork_settings.json"
	}
	return filepath.Join(config, filename), nil
}
