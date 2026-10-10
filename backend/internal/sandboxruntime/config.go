// Package sandboxruntime ports the installed sandbox-runtime 0.0.44 primitives
// and the original CLI adapter. Native policy and lifecycle remain Go; Source
// JavaScript is used solely by differential tests.
package sandboxruntime

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
)

type Config struct {
	Network                      NetworkConfig       `json:"network"`
	Filesystem                   FilesystemConfig    `json:"filesystem"`
	IgnoreViolations             json.RawMessage     `json:"ignoreViolations,omitempty"`
	EnableWeakerNestedSandbox    *bool               `json:"enableWeakerNestedSandbox,omitempty"`
	EnableWeakerNetworkIsolation *bool               `json:"enableWeakerNetworkIsolation,omitempty"`
	Ripgrep                      protocol.JSONObject `json:"ripgrep"`
}
type NetworkConfig struct {
	AllowedDomains      []string  `json:"allowedDomains"`
	DeniedDomains       []string  `json:"deniedDomains"`
	AllowUnixSockets    *[]string `json:"allowUnixSockets,omitempty"`
	AllowAllUnixSockets *bool     `json:"allowAllUnixSockets,omitempty"`
	AllowLocalBinding   *bool     `json:"allowLocalBinding,omitempty"`
	HTTPProxyPort       *float64  `json:"httpProxyPort,omitempty"`
	SOCKSProxyPort      *float64  `json:"socksProxyPort,omitempty"`
}
type FilesystemConfig struct {
	DenyRead   []string `json:"denyRead"`
	AllowRead  []string `json:"allowRead"`
	AllowWrite []string `json:"allowWrite"`
	DenyWrite  []string `json:"denyWrite"`
}
type SettingsSource struct {
	Name, Root, File string
	Settings         protocol.JSONObject
}
type ConfigInput struct {
	Settings                                                                                   protocol.JSONObject
	Sources                                                                                    []SettingsSource
	Home, CWD, OriginalCWD, TemporaryDirectory, ManagedDropInDirectory, MainWorktreeRepository string
	AdditionalDirectories                                                                      []string
	Ripgrep                                                                                    protocol.JSONObject
	Stat                                                                                       func(string) error
}
type ConvertedConfig struct {
	Config     Config
	ScrubPaths []string
}

var permissionRule = regexp.MustCompile(`^([^(]+)\(([^)]+)\)$`)

func rule(value string) (name, contents string) {
	parts := permissionRule.FindStringSubmatch(value)
	if len(parts) != 3 {
		return value, ""
	}
	return parts[1], parts[2]
}
func object(o protocol.JSONObject, name string) protocol.JSONObject { v, _ := o.Object(name); return v }
func texts(o protocol.JSONObject, name string) []string {
	result := []string{}
	_ = json.Unmarshal(o.Get(name), &result)
	return result
}
func textsPointer(o protocol.JSONObject, name string) *[]string {
	if o.Get(name) == nil {
		return nil
	}
	value := texts(o, name)
	return &value
}
func boolPointer(o protocol.JSONObject, name string) *bool {
	if o.Get(name) == nil {
		return nil
	}
	var v bool
	_ = json.Unmarshal(o.Get(name), &v)
	return &v
}
func floatPointer(o protocol.JSONObject, name string) *float64 {
	if o.Get(name) == nil {
		return nil
	}
	var v float64
	_ = json.Unmarshal(o.Get(name), &v)
	return &v
}
func truth(o protocol.JSONObject, name string) bool { return string(o.Get(name)) == "true" }
func resolveRulePath(pattern, root string) string {
	if strings.HasPrefix(pattern, "//") {
		return pattern[1:]
	}
	if strings.HasPrefix(pattern, "/") {
		return filepath.Join(root, pattern[1:])
	}
	return pattern
}
func resolveFilesystemPath(pattern, root, home string) (string, error) {
	if strings.HasPrefix(pattern, "//") {
		return pattern[1:], nil
	}
	if strings.ContainsRune(pattern, 0) || strings.ContainsRune(root, 0) {
		return "", errors.New("Path contains null bytes")
	}
	pattern = strings.TrimFunc(pattern, jsWhitespace)
	var result string
	switch {
	case pattern == "":
		result = filepath.Clean(root)
	case pattern == "~":
		result = home
	case strings.HasPrefix(pattern, "~/"):
		result = filepath.Join(home, pattern[2:])
	case filepath.IsAbs(pattern):
		result = filepath.Clean(pattern)
	default:
		result = filepath.Join(root, pattern)
	}
	return norm.NFC.String(result), nil
}
func jsWhitespace(r rune) bool {
	return r >= 9 && r <= 13 || r == 0x20 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
}

// Convert preserves Source's source order, independent managed-domain/read
// policy, literal cwd vs originalCwd distinction, and per-source path roots.
func Convert(in ConfigInput) (ConvertedConfig, error) {
	if in.Stat == nil || !filepath.IsAbs(in.Home) || !filepath.IsAbs(in.CWD) || !filepath.IsAbs(in.OriginalCWD) || in.TemporaryDirectory == "" || !in.Ripgrep.IsObject() {
		return ConvertedConfig{}, errors.New("sandbox converter requires actual roots/files/ripgrep")
	}
	sandbox := object(in.Settings, "sandbox")
	network := object(sandbox, "network")
	permissions := object(in.Settings, "permissions")
	policy := protocol.NewJSONObject()
	for _, s := range in.Sources {
		if s.Name == "policySettings" {
			policy = s.Settings
		}
	}
	policySandbox := object(policy, "sandbox")
	policyNetwork := object(policySandbox, "network")
	policyFilesystem := object(policySandbox, "filesystem")
	managedOnly := truth(policyNetwork, "allowManagedDomainsOnly")
	domainSettings := in.Settings
	domainNetwork := network
	if managedOnly {
		domainSettings = policy
		domainNetwork = policyNetwork
	}
	allowed := append([]string{}, texts(domainNetwork, "allowedDomains")...)
	for _, v := range texts(object(domainSettings, "permissions"), "allow") {
		name, contents := rule(v)
		if name == "WebFetch" && strings.HasPrefix(contents, "domain:") {
			allowed = append(allowed, strings.TrimPrefix(contents, "domain:"))
		}
	}
	denied := []string{}
	for _, v := range texts(permissions, "deny") {
		name, contents := rule(v)
		if name == "WebFetch" && strings.HasPrefix(contents, "domain:") {
			denied = append(denied, strings.TrimPrefix(contents, "domain:"))
		}
	}
	fs := FilesystemConfig{AllowWrite: []string{".", in.TemporaryDirectory}, DenyWrite: []string{}, DenyRead: []string{}, AllowRead: []string{}}
	for _, s := range in.Sources {
		if s.File != "" {
			fs.DenyWrite = append(fs.DenyWrite, s.File)
		}
	}
	fs.DenyWrite = append(fs.DenyWrite, in.ManagedDropInDirectory)
	if in.CWD != in.OriginalCWD {
		fs.DenyWrite = append(fs.DenyWrite, filepath.Join(in.CWD, ".claude/settings.json"), filepath.Join(in.CWD, ".claude/settings.local.json"))
	}
	fs.DenyWrite = append(fs.DenyWrite, filepath.Join(in.OriginalCWD, ".claude/skills"))
	if in.CWD != in.OriginalCWD {
		fs.DenyWrite = append(fs.DenyWrite, filepath.Join(in.CWD, ".claude/skills"))
	}
	scrub := []string{}
	dirs := []string{in.OriginalCWD}
	if in.CWD != in.OriginalCWD {
		dirs = append(dirs, in.CWD)
	}
	for _, dir := range dirs {
		for _, name := range []string{"HEAD", "objects", "refs", "hooks", "config"} {
			p := filepath.Join(dir, name)
			if in.Stat(p) == nil {
				fs.DenyWrite = append(fs.DenyWrite, p)
			} else {
				scrub = append(scrub, p)
			}
		}
	}
	if in.MainWorktreeRepository != "" && in.MainWorktreeRepository != in.CWD {
		fs.AllowWrite = append(fs.AllowWrite, in.MainWorktreeRepository)
	}
	seen := map[string]bool{}
	for _, p := range append(texts(permissions, "additionalDirectories"), in.AdditionalDirectories...) {
		if !seen[p] {
			fs.AllowWrite = append(fs.AllowWrite, p)
			seen[p] = true
		}
	}
	for _, s := range in.Sources {
		sourcePermissions := object(s.Settings, "permissions")
		for _, v := range texts(sourcePermissions, "allow") {
			name, contents := rule(v)
			if name == "Edit" && contents != "" {
				fs.AllowWrite = append(fs.AllowWrite, resolveRulePath(contents, s.Root))
			}
		}
		for _, v := range texts(sourcePermissions, "deny") {
			name, contents := rule(v)
			if contents == "" {
				continue
			}
			if name == "Edit" {
				fs.DenyWrite = append(fs.DenyWrite, resolveRulePath(contents, s.Root))
			}
			if name == "Read" {
				fs.DenyRead = append(fs.DenyRead, resolveRulePath(contents, s.Root))
			}
		}
		sourceFS := object(object(s.Settings, "sandbox"), "filesystem")
		for _, part := range []struct {
			Name   string
			Target *[]string
		}{{"allowWrite", &fs.AllowWrite}, {"denyWrite", &fs.DenyWrite}, {"denyRead", &fs.DenyRead}, {"allowRead", &fs.AllowRead}} {
			if part.Name == "allowRead" && truth(policyFilesystem, "allowManagedReadPathsOnly") && s.Name != "policySettings" {
				continue
			}
			for _, p := range texts(sourceFS, part.Name) {
				resolved, e := resolveFilesystemPath(p, s.Root, in.Home)
				if e != nil {
					return ConvertedConfig{}, e
				}
				*part.Target = append(*part.Target, resolved)
			}
		}
	}
	ripgrep := in.Ripgrep.Clone()
	if v := sandbox.Get("ripgrep"); v != nil {
		_ = ripgrep.UnmarshalJSON(v)
	}
	return ConvertedConfig{Config: Config{Network: NetworkConfig{AllowedDomains: allowed, DeniedDomains: denied, AllowUnixSockets: textsPointer(network, "allowUnixSockets"), AllowAllUnixSockets: boolPointer(network, "allowAllUnixSockets"), AllowLocalBinding: boolPointer(network, "allowLocalBinding"), HTTPProxyPort: floatPointer(network, "httpProxyPort"), SOCKSProxyPort: floatPointer(network, "socksProxyPort")}, Filesystem: fs, IgnoreViolations: append(json.RawMessage(nil), sandbox.Get("ignoreViolations")...), EnableWeakerNestedSandbox: boolPointer(sandbox, "enableWeakerNestedSandbox"), EnableWeakerNetworkIsolation: boolPointer(sandbox, "enableWeakerNetworkIsolation"), Ripgrep: ripgrep}, ScrubPaths: scrub}, nil
}
