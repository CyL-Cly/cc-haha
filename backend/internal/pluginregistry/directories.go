package pluginregistry

import (
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
)

type Directories struct {
	Primary string   `json:"primary"`
	Seeds   []string `json:"seeds"`
}

// ResolveDirectories uses explicit home/config/session state; it never reads
// the host's environment or discovers a real user's home.
func ResolveDirectories(home, config string, cowork bool, environment func(string) string) Directories {
	expand := func(value string) string {
		if value == "~" || strings.HasPrefix(value, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(value, `~\`)) {
			return home + value[1:]
		}
		return value
	}
	name := "plugins"
	if cowork || envTruthy(environment("CLAUDE_CODE_USE_COWORK_PLUGINS")) {
		name = "cowork_plugins"
	}
	d := Directories{Primary: filepath.Join(config, name), Seeds: []string{}}
	if override := environment("CLAUDE_CODE_PLUGIN_CACHE_DIR"); override != "" {
		d.Primary = expand(override)
	}
	for _, seed := range strings.Split(environment("CLAUDE_CODE_PLUGIN_SEED_DIR"), string(filepath.ListSeparator)) {
		if seed != "" {
			d.Seeds = append(d.Seeds, expand(seed))
		}
	}
	return d
}

func VersionedPathIn(base, id, version string) string {
	name, market := nameAndMarket(id)
	if market == "" {
		market = "unknown"
	}
	if name == "" {
		name = id
	}
	return filepath.Join(base, "cache", sanitizeCacheComponent(market, false, false), sanitizeCacheComponent(name, false, false), sanitizeCacheComponent(version, true, false))
}

func sanitizeCacheComponent(value string, dot, at bool) string {
	var result strings.Builder
	for _, unit := range utf16.Encode([]rune(value)) {
		if unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' || unit >= '0' && unit <= '9' || unit == '-' || unit == '_' || dot && unit == '.' || at && unit == '@' {
			result.WriteByte(byte(unit))
		} else {
			result.WriteByte('-')
		}
	}
	return result.String()
}
