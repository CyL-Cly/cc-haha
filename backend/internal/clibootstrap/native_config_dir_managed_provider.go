package clibootstrap

import (
	"encoding/json"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"golang.org/x/text/unicode/norm"
)

// This uncached filesystem consumer follows the live scalar authority. A
// warmed Global/Project/Plans memo and any admitted transcript writer retain
// their own Source lifetime; changing this reader cannot rebind those objects.
func (m *MainPrint) nativeCcHahaConfigDirectory() (string, error) {
	if m == nil || m.nativeSettings == nil || m.options.Native == nil || !m.options.Native.NativeProcess || m.nativeSettings.n != m.options.Native {
		return "", mainCap("native cc-haha current-directory bindings")
	}
	return nativeCcHahaFileDirectory(m.options.Native, m.environment)
}

func (p *NativeProcessSettings) nativeCcHahaConfigDirectory() (string, error) {
	if p == nil || p.n == nil || !p.n.NativeProcess || p.environment == nil {
		return "", mainCap("native settings cc-haha current-directory bindings")
	}
	return nativeCcHahaFileDirectory(p.n, p.environment)
}

func nativeCcHahaFileDirectory(n *NativeBoundary, environment *clistartupenv.Environment) (string, error) {
	directory, present := n.nativeEnvironmentValue("CLAUDE_CONFIG_DIR")
	if environment != nil {
		raw := environment.Get("CLAUDE_CONFIG_DIR")
		present = raw != nil
		if present {
			if err := json.Unmarshal(raw, &directory); err != nil {
				return "", err
			}
		}
	}
	if !present {
		directory = filepath.Join(n.Home, ".claude")
	}
	directory = norm.NFC.String(directory)
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(n.CWD, directory)
	}
	return directory, nil
}
