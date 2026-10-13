package sdkpermissions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowspaths"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

type NativeStartupOptions struct {
	SettingsForSource                func(string) (*protocol.JSONObject, error)
	InitialSettings                  func() (*protocol.JSONObject, error)
	Home, CWD, OriginalCWD, Platform string
	Environment                      func(string) string
	Global                           func() (*protocol.JSONObject, error)
	Policy                           func() (*protocol.JSONObject, error)
	Stat, Lstat                      func(string) (fs.FileInfo, error)
	Realpath                         func(string) (string, error)
}
type NativeStartupRequest struct {
	Mode                                                        string
	DangerouslySkipPermissions, AllowDangerouslySkipPermissions bool
	AdditionalDirectories                                       []string
}
type NativeStartupResult struct {
	Mode, Notification                                      string
	Warnings, AdditionalDirectories, InstructionDirectories []string
	WorkingDirectories                                      json.RawMessage
	BypassAvailable, AutoAvailable                          bool
}

// NativeStartup owns Source initialPermissionModeFromCLI and the workspace
// portion of initializeToolPermissionContext. Its real FS/environment/config
// producers are explicit, and its prepared result belongs to one Harness.
type NativeStartup struct {
	owner    *cooperative.Owner
	options  NativeStartupOptions
	prepared *NativeStartupResult
}

func NewNativeStartup(owner *cooperative.Owner, o NativeStartupOptions) (*NativeStartup, error) {
	absolute := filepath.IsAbs
	if o.Platform == "windows" {
		absolute = windowspaths.IsWindowsAbsolute
	}
	if owner == nil || !absolute(o.Home) || !absolute(o.CWD) || !absolute(o.OriginalCWD) || o.Environment == nil || o.Global == nil || o.Stat == nil || o.Lstat == nil || o.Realpath == nil {
		return nil, errors.New("native permissions require owned home/cwd/environment/global/stat/lstat/realpath producers")
	}
	if o.Platform != "darwin" && o.Platform != "linux" && o.Platform != "windows" {
		return nil, cap("native permission startup platform")
	}
	return &NativeStartup{owner: owner, options: o}, nil
}
func (s *NativeStartup) Owns(owner *cooperative.Owner) bool {
	return s != nil && s.owner == owner && owner != nil && s.prepared != nil
}
func (s *NativeStartup) Result(scope *cooperative.Scope) (NativeStartupResult, error) {
	if s == nil || !scope.BelongsTo(s.owner) || s.prepared == nil {
		return NativeStartupResult{}, cooperative.ErrRestricted
	}
	if e := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil }); e != nil {
		return NativeStartupResult{}, e
	}
	r := *s.prepared
	r.Warnings = append([]string{}, r.Warnings...)
	r.AdditionalDirectories = append([]string{}, r.AdditionalDirectories...)
	r.InstructionDirectories = append([]string{}, r.InstructionDirectories...)
	r.WorkingDirectories = append(json.RawMessage(nil), r.WorkingDirectories...)
	return r, nil
}
func startupTruthy(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) || bytes.Equal(value, []byte("false")) {
		return false
	}
	if value[0] == '"' {
		units, e := protocol.DecodeJavaScriptJSONString(value)
		return e == nil && len(units) > 0
	}
	if value[0] == '{' || value[0] == '[' || bytes.Equal(value, []byte("true")) {
		return true
	}
	var number float64
	return json.Unmarshal(value, &number) == nil && number != 0 && !math.IsNaN(number)
}
func startupEnvTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func (s *NativeStartup) cachedDisableBypass() (bool, error) {
	if s.options.Environment("USER_TYPE") == "ant" {
		var overrides protocol.JSONObject
		if overrides.UnmarshalJSON([]byte(s.options.Environment("CLAUDE_INTERNAL_FC_OVERRIDES"))) == nil && overrides.IsObject() {
			if value := overrides.Get("tengu_disable_bypass_permissions_mode"); value != nil {
				return startupTruthy(value), nil
			}
		}
		global, e := s.options.Global()
		if e != nil {
			// Original getConfigOverrides catches early/unreadable config and
			// resolves undefined, before the external empty-key gate exit.
			return false, nil
		}
		if global != nil {
			if overrides.UnmarshalJSON(global.Get("growthBookOverrides")) == nil && overrides.IsObject() {
				if value := overrides.Get("tengu_disable_bypass_permissions_mode"); value != nil {
					return startupTruthy(value), nil
				}
			}
		}
	}
	// Actual external Source src/constants/keys.ts always returns an empty
	// GrowthBook client key. The cached getter therefore exits before reading
	// cachedGrowthBookFeatures/cachedStatsigGates, even if they contain true.
	return false, nil
}
func startupPermissions(settings *protocol.JSONObject) protocol.JSONObject {
	out := protocol.NewJSONObject()
	if settings != nil {
		_ = out.UnmarshalJSON(settings.Get("permissions"))
	}
	return out
}
func (s *NativeStartup) mode(settings *protocol.JSONObject, r NativeStartupRequest) (string, string, bool, error) {
	gate, e := s.cachedDisableBypass()
	if e != nil {
		return "", "", false, e
	}
	permissions := startupPermissions(settings)
	disabled := permissions.String("disableBypassPermissionsMode") == "disable" || gate
	candidates := []string{}
	if r.DangerouslySkipPermissions {
		candidates = append(candidates, "bypassPermissions")
	}
	if r.Mode != "" {
		mode := r.Mode
		switch mode {
		case "default", "acceptEdits", "plan", "bypassPermissions", "dontAsk", "auto":
		default:
			mode = "default"
		}
		candidates = append(candidates, mode)
	}
	if mode := permissions.String("defaultMode"); mode != "" {
		if !startupEnvTruthy(s.options.Environment("CLAUDE_CODE_REMOTE")) || mode == "acceptEdits" || mode == "plan" || mode == "default" {
			candidates = append(candidates, mode)
		}
	}
	notification := ""
	selected := "default"
	for _, mode := range candidates {
		if mode == "auto" && s.autoEnabledState() == "disabled" {
			continue
		}
		if mode == "bypassPermissions" && disabled {
			if gate {
				notification = "Bypass permissions mode was disabled by your organization policy"
			} else {
				notification = "Bypass permissions mode was disabled by settings"
			}
			continue
		}
		selected = mode
		break
	}
	return selected, notification, (selected == "bypassPermissions" || r.AllowDangerouslySkipPermissions) && !disabled, nil
}
func (s *NativeStartup) expand(raw string) (string, error) {
	if s.options.Platform == "windows" {
		path, err := windowspaths.ExpandWindows(raw, s.options.CWD, s.options.Home)
		if err != nil {
			return "", err
		}
		// validateDirectoryForWorkspace resolves again after expandPath, dropping
		// an absolute input's trailing separator before using its Map key.
		return windowspaths.ResolveWindows(path, s.options.CWD), nil
	}
	if strings.ContainsRune(raw, 0) || strings.ContainsRune(s.options.CWD, 0) {
		return "", errors.New("Path contains null bytes")
	}
	value := strings.TrimFunc(raw, func(r rune) bool {
		return r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
	if value == "" {
		value = s.options.CWD
	} else if value == "~" {
		value = s.options.Home
	} else if strings.HasPrefix(value, "~/") {
		value = filepath.Join(s.options.Home, value[2:])
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(s.options.CWD, value)
	}
	return norm.NFC.String(filepath.Clean(value)), nil
}
func startupWorkingPath(path, root string) bool {
	aliases := func(value string) string {
		if strings.HasPrefix(value, "/private/var/") {
			value = strings.TrimPrefix(value, "/private")
		}
		if value == "/private/tmp" || strings.HasPrefix(value, "/private/tmp/") {
			value = strings.TrimPrefix(value, "/private")
		}
		return cases.Lower(language.Und).String(value)
	}
	rel, e := filepath.Rel(aliases(root), aliases(path))
	if e != nil {
		return false
	}
	return !regexp.MustCompile(`(?:^|[\\/])\.\.(?:[\\/]|$)`).MatchString(rel) && !filepath.IsAbs(rel)
}
func (s *NativeStartup) workingPath(path, root string) bool {
	if s.options.Platform != "windows" {
		return startupWorkingPath(path, root)
	}
	path, pathErr := windowspaths.ExpandWindows(path, s.options.CWD, s.options.Home)
	root, rootErr := windowspaths.ExpandWindows(root, s.options.CWD, s.options.Home)
	if pathErr != nil || rootErr != nil {
		return false
	}
	lower := cases.Lower(language.Und)
	relative := windowspaths.RelativeWindows(lower.String(root), lower.String(path))
	return relative == "" || relative != ".." && !regexp.MustCompile(`(?:^|[\\/])\.\.(?:[\\/]|$)`).MatchString(relative)
}
func (s *NativeStartup) dir(path string) string {
	if s.options.Platform == "windows" {
		return windowspaths.DirWindows(path)
	}
	return filepath.Dir(path)
}
func (s *NativeStartup) clean(path string) string {
	if s.options.Platform == "windows" {
		return windowspaths.CleanWindows(path)
	}
	return filepath.Clean(path)
}
func (s *NativeStartup) pwdAlias() string {
	pwd := s.options.Environment("PWD")
	if pwd == "" || pwd == s.options.OriginalCWD || strings.HasPrefix(pwd, "//") || strings.HasPrefix(pwd, `\\`) {
		return ""
	}
	info, e := s.options.Lstat(pwd)
	if e != nil || info.Mode()&(os.ModeNamedPipe|os.ModeSocket|os.ModeDevice) != 0 {
		return ""
	}
	resolved, e := s.options.Realpath(pwd)
	if e != nil || resolved == pwd || resolved != s.clean(s.options.OriginalCWD) {
		return ""
	}
	return pwd
}
func (s *NativeStartup) Prepare(scope *cooperative.Scope, settings *protocol.JSONObject, r NativeStartupRequest) (NativeStartupResult, error) {
	if s == nil || !scope.BelongsTo(s.owner) || s.prepared != nil {
		return NativeStartupResult{}, cooperative.ErrRestricted
	}
	if e := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil }); e != nil {
		return NativeStartupResult{}, e
	}
	mode, notification, bypass, e := s.mode(settings, r)
	if e != nil {
		return NativeStartupResult{}, e
	}
	permissions := startupPermissions(settings)
	var configured []string
	if value := permissions.Get("additionalDirectories"); value != nil {
		var paths []json.RawMessage
		if e = json.Unmarshal(value, &paths); e != nil {
			return NativeStartupResult{}, e
		}
		for _, path := range paths {
			units, e := protocol.DecodeJavaScriptJSONString(path)
			if e != nil {
				return NativeStartupResult{}, e
			}
			for i, unit := range units {
				if unit >= 0xd800 && unit <= 0xdbff && (i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff) || unit >= 0xdc00 && unit <= 0xdfff && (i == 0 || units[i-1] < 0xd800 || units[i-1] > 0xdbff) {
					return NativeStartupResult{}, cap("native startup unpaired UTF-16 filesystem directory")
				}
			}
			configured = append(configured, string(utf16.Decode(units)))
		}
	}
	paths := append(append([]string{}, configured...), r.AdditionalDirectories...)
	base := []string{s.options.OriginalCWD}
	entries := [][]json.RawMessage{}
	if pwd := s.pwdAlias(); pwd != "" {
		base = append(base, pwd)
		entries = append(entries, []json.RawMessage{quote(pwd), persistRaw(map[string]string{"path": pwd, "source": "session"})})
	}
	directories, warnings := []string{}, []string{}
	for _, raw := range paths {
		if raw == "" {
			warnings = append(warnings, "Please provide a directory path.")
			continue
		}
		absolute, e := s.expand(raw)
		if e != nil {
			return NativeStartupResult{}, e
		}
		info, e := s.options.Stat(absolute)
		if e != nil {
			if errors.Is(e, fs.ErrNotExist) || errors.Is(e, syscall.ENOTDIR) || errors.Is(e, syscall.EACCES) || errors.Is(e, syscall.EPERM) {
				continue
			}
			return NativeStartupResult{}, e
		}
		if !info.IsDir() {
			warnings = append(warnings, fmt.Sprintf("%s is not a directory. Did you mean to add the parent directory %s?", raw, s.dir(absolute)))
			continue
		}
		covered := false
		for _, root := range base {
			if s.workingPath(absolute, root) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		directories = append(directories, absolute)
	}
	// Source validates every candidate against the same original context, then
	// applies results serially. Nested candidates both succeed; Map.set keeps
	// the first insertion position when a later spelling resolves to that key.
	for _, path := range directories {
		value := persistRaw(map[string]string{"path": path, "source": "cliArg"})
		found := false
		for i, entry := range entries {
			if rawStringIdentity(entry[0]) == rawStringIdentity(quote(path)) {
				entries[i][1] = value
				found = true
				break
			}
		}
		if !found {
			entries = append(entries, []json.RawMessage{quote(path), value})
		}
	}
	keys := []string{}
	for _, entry := range entries {
		var key string
		_ = json.Unmarshal(entry[0], &key)
		keys = append(keys, key)
	}
	auto := permissions.String("disableAutoMode") != "disable" && (settings == nil || settings.String("disableAutoMode") != "disable")
	result := NativeStartupResult{Mode: mode, Notification: notification, Warnings: warnings, AdditionalDirectories: keys, InstructionDirectories: append([]string{}, r.AdditionalDirectories...), WorkingDirectories: persistRaw(map[string]any{"map": entries}), BypassAvailable: bypass, AutoAvailable: auto}
	s.prepared = &result
	return s.Result(scope)
}
func (s *NativeStartup) apply(scope *cooperative.Scope, context *protocol.JSONObject) error {
	r, e := s.Result(scope)
	if e != nil {
		return e
	}
	context.Set("additionalWorkingDirectories", r.WorkingDirectories)
	context.Set("isBypassPermissionsModeAvailable", persistRaw(r.BypassAvailable))
	context.Set("isAutoModeAvailable", persistRaw(r.AutoAvailable))
	return nil
}
