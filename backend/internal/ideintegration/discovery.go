// Package ideintegration owns the original local IDE lockfile discovery. Native
// filesystem, process and network observations are supplied explicitly.
package ideintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
)

type Lockfile struct {
	Path      string
	Modified  float64
	Data      []byte
	ReadError error
}
type IDE struct {
	Name                            string   `json:"name"`
	Port                            float64  `json:"port"`
	WorkspaceFolders                []string `json:"workspaceFolders"`
	URL                             string   `json:"url"`
	Valid                           bool     `json:"isValid"`
	AuthToken                       string   `json:"authToken,omitempty"`
	RunningInWindows                bool     `json:"ideRunningInWindows"`
	nameJSON, authJSON, foldersJSON json.RawMessage
}

func (i IDE) NameJSON() json.RawMessage {
	if i.nameJSON != nil {
		return append(json.RawMessage(nil), i.nameJSON...)
	}
	b, _ := json.Marshal(i.Name)
	return b
}
func (i IDE) AuthJSON() json.RawMessage { return append(json.RawMessage(nil), i.authJSON...) }
func (i IDE) MarshalJSON() ([]byte, error) {
	port := json.RawMessage("null")
	if !math.IsNaN(i.Port) && !math.IsInf(i.Port, 0) {
		port = []byte(numberText(i.Port))
	}
	name := i.NameJSON()
	folders := i.foldersJSON
	if folders == nil {
		folders, _ = json.Marshal(i.WorkspaceFolders)
	}
	url, _ := json.Marshal(i.URL)
	valid, _ := json.Marshal(i.Valid)
	windows, _ := json.Marshal(i.RunningInWindows)
	o := protocol.NewJSONObject(protocol.JSONField{Name: "url", Value: url}, protocol.JSONField{Name: "name", Value: name}, protocol.JSONField{Name: "workspaceFolders", Value: folders}, protocol.JSONField{Name: "port", Value: port}, protocol.JSONField{Name: "isValid", Value: valid})
	if i.authJSON != nil {
		o.Set("authToken", i.authJSON)
	}
	o.Set("ideRunningInWindows", windows)
	return o.MarshalJSON()
}

type Options struct {
	CWD, Platform, Terminal string
	Environment             map[string]string
	ParentPID               int
	ReadLockfiles           func(context.Context) ([]Lockfile, error)
	Remove                  func(string) error
	ProcessRunning          func(int) bool
	Ancestors               func(context.Context, int, int) ([]int, error)
	Probe                   func(context.Context, string, float64) bool
	Host                    func(context.Context, bool, float64) string
	LocalPath               func(string, string) string
	Now                     func() time.Time
	Sleep                   func(context.Context, time.Duration) error
}
type Service struct{ options Options }

func New(o Options) *Service {
	env := map[string]string{}
	for k, v := range o.Environment {
		env[k] = v
	}
	o.Environment = env
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-timer.C:
				return nil
			}
		}
	}
	return &Service{options: o}
}
func TruthyEnv(value string) bool {
	switch strings.ToLower(protocol.TrimJavaScriptString(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func DefinedFalsy(value string, defined bool) bool {
	if !defined {
		return false
	}
	switch strings.ToLower(protocol.TrimJavaScriptString(value)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

var displayNames = map[string]string{"cursor": "Cursor", "windsurf": "Windsurf", "vscode": "VS Code", "intellij": "IntelliJ IDEA", "pycharm": "PyCharm", "webstorm": "WebStorm", "phpstorm": "PhpStorm", "rubymine": "RubyMine", "clion": "CLion", "goland": "GoLand", "rider": "Rider", "datagrip": "DataGrip", "appcode": "AppCode", "dataspell": "DataSpell", "aqua": "Aqua", "gateway": "Gateway", "fleet": "Fleet", "androidstudio": "Android Studio"}

func SupportedTerminal(terminal string, env map[string]string) bool {
	_, ok := displayNames[terminal]
	return ok || env["FORCE_CODE_TERMINAL"] != ""
}
func AutoConnect(flag, global bool, terminal string, env map[string]string) bool {
	value, defined := env["CLAUDE_CODE_AUTO_CONNECT_IDE"]
	return (flag || global || SupportedTerminal(terminal, env) || env["CLAUDE_CODE_SSE_PORT"] != "" || TruthyEnv(value)) && !DefinedFalsy(value, defined)
}
func Terminal(env map[string]string) string {
	if strings.Contains(env["VSCODE_GIT_ASKPASS_MAIN"], "cursor") {
		return "cursor"
	}
	if strings.Contains(env["VSCODE_GIT_ASKPASS_MAIN"], "windsurf") {
		return "windsurf"
	}
	if strings.Contains(env["VSCODE_GIT_ASKPASS_MAIN"], "antigravity") {
		return "antigravity"
	}
	bundle := strings.ToLower(env["__CFBundleIdentifier"])
	if strings.Contains(bundle, "vscodium") {
		return "codium"
	}
	if strings.Contains(bundle, "windsurf") {
		return "windsurf"
	}
	if strings.Contains(bundle, "com.google.android.studio") {
		return "androidstudio"
	}
	for _, name := range []string{"intellij", "pycharm", "webstorm", "phpstorm", "rubymine", "clion", "goland", "rider", "datagrip", "appcode", "dataspell", "aqua", "gateway", "fleet", "androidstudio"} {
		if strings.Contains(bundle, name) {
			return name
		}
	}
	if env["VisualStudioVersion"] != "" {
		return "visualstudio"
	}
	if env["TERMINAL_EMULATOR"] == "JetBrains-JediTerm" {
		return "pycharm"
	}
	if env["TERM"] == "xterm-ghostty" {
		return "ghostty"
	}
	if strings.Contains(env["TERM"], "kitty") {
		return "kitty"
	}
	if env["TERM_PROGRAM"] != "" {
		return env["TERM_PROGRAM"]
	}
	if env["TMUX"] != "" {
		return "tmux"
	}
	if env["STY"] != "" {
		return "screen"
	}
	return ""
}
func integerPrefix(value string) float64 {
	value = protocol.TrimJavaScriptString(value)
	end := 0
	if len(value) > 0 && (value[0] == '-' || value[0] == '+') {
		end++
	}
	start := end
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == start {
		return math.NaN()
	}
	n, _ := strconv.ParseFloat(value[:end], 64)
	return n
}
func numberText(n float64) string {
	if math.IsNaN(n) {
		return "NaN"
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

type parsedLock struct {
	IDE
	pid                 int
	pidTruthy, pidValid bool
	invalidFolders      bool
	folders             []json.RawMessage
}

func (s *Service) parse(row Lockfile) (parsedLock, bool) {
	if row.ReadError != nil {
		return parsedLock{}, false
	}
	p := parsedLock{IDE: IDE{WorkspaceFolders: []string{}, Port: integerPrefix(strings.Replace(s.lockName(row.Path), ".lock", "", 1))}}
	var object protocol.JSONObject
	decoded := []byte(protocol.DecodeJavaScriptUTF8(row.Data))
	if trimmed := bytes.TrimSpace(decoded); len(trimmed) > 0 && trimmed[0] == '{' && object.UnmarshalJSON(decoded) == nil {
		if values := object.Get("workspaceFolders"); openaiauth.AuthTruthy(values) {
			p.foldersJSON = append(json.RawMessage(nil), values...)
			var folders []json.RawMessage
			if json.Unmarshal(values, &folders) != nil {
				p.invalidFolders = true
			} else {
				p.folders = folders
				for _, folder := range folders {
					if !openaiauth.AuthTruthy(folder) {
						continue
					}
					var value string
					if json.Unmarshal(folder, &value) != nil {
						p.invalidFolders = true
						break
					}
					p.WorkspaceFolders = append(p.WorkspaceFolders, value)
				}
			}
		}
		pidRaw := object.Get("pid")
		p.pidTruthy = openaiauth.AuthTruthy(pidRaw)
		var pid float64
		if len(pidRaw) > 0 && pidRaw[0] != '"' && json.Unmarshal(pidRaw, &pid) == nil && math.Trunc(pid) == pid && pid <= math.MaxInt32 && pid >= math.MinInt32 {
			p.pid = int(pid)
			p.pidValid = true
		}
		p.nameJSON = object.Get("ideName")
		if string(p.nameJSON) == "null" {
			p.nameJSON = nil
		}
		p.Name = object.String("ideName")
		p.authJSON = object.Get("authToken")
		p.AuthToken = object.String("authToken")
		p.RunningInWindows = string(object.Get("runningInWindows")) == "true"
		transport := object.String("transport")
		if transport == "ws" {
			p.URL = "ws"
		} else {
			p.URL = "http"
		}
		return p, true
	}
	var primitive any
	if json.Unmarshal(decoded, &primitive) == nil && primitive != nil {
		p.URL = "http"
		return p, true
	}
	for _, line := range strings.Split(string(protocol.DecodeJavaScriptUTF8(row.Data)), "\n") {
		p.WorkspaceFolders = append(p.WorkspaceFolders, protocol.TrimJavaScriptString(line))
		b, _ := json.Marshal(protocol.TrimJavaScriptString(line))
		p.folders = append(p.folders, b)
	}
	p.URL = "http"
	return p, true
}
func (s *Service) lockfiles(ctx context.Context) ([]Lockfile, error) {
	rows, e := s.options.ReadLockfiles(ctx)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Modified > rows[j].Modified })
	return rows, e
}
func (s *Service) host(ctx context.Context, p parsedLock) string {
	if s.options.Host != nil {
		return s.options.Host(ctx, p.RunningInWindows, p.Port)
	}
	if override := s.options.Environment["CLAUDE_CODE_IDE_HOST_OVERRIDE"]; override != "" {
		return override
	}
	return "127.0.0.1"
}
func (s *Service) Cleanup(ctx context.Context) error {
	rows, e := s.lockfiles(ctx)
	if e != nil {
		return e
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		p, ok := s.parse(row)
		remove := !ok
		if ok {
			host := s.host(ctx, p)
			if p.pidTruthy {
				if !p.pidValid || !s.options.ProcessRunning(p.pid) {
					remove = s.options.Platform != "wsl" || !s.options.Probe(ctx, host, p.Port)
				}
			} else {
				remove = !s.options.Probe(ctx, host, p.Port)
			}
		}
		if remove && s.options.Remove != nil {
			_ = s.options.Remove(row.Path)
		}
	}
	return nil
}
func (s *Service) Detect(ctx context.Context, includeInvalid bool) ([]IDE, error) {
	result := []IDE{}
	rows, e := s.lockfiles(ctx)
	if e != nil {
		return result, e
	}
	o := s.options
	cwd := norm.NFC.String(o.CWD)
	envPort := integerPrefix(o.Environment["CLAUDE_CODE_SSE_PORT"])
	supported := SupportedTerminal(o.Terminal, o.Environment)
	ancestry := o.Platform != "wsl" && supported
	var parents map[int]bool
	for _, row := range rows {
		if ctx.Err() != nil {
			return result, context.Cause(ctx)
		}
		p, ok := s.parse(row)
		if !ok {
			continue
		}
		valid := TruthyEnv(o.Environment["CLAUDE_CODE_IDE_SKIP_VALID_CHECK"]) || p.Port == envPort
		if !valid {
			if p.invalidFolders && p.folders == nil {
				return result, nil
			}
			for _, rawFolder := range p.folders {
				if !openaiauth.AuthTruthy(rawFolder) {
					continue
				}
				var folder string
				if json.Unmarshal(rawFolder, &folder) != nil {
					return result, nil
				}
				local := folder
				if o.Platform == "wsl" && p.RunningInWindows && o.Environment["WSL_DISTRO_NAME"] != "" {
					distro := o.Environment["WSL_DISTRO_NAME"]
					if !DistroMatches(folder, distro) {
						continue
					}
					if pathWithin(cwd, resolve(folder, o.CWD, o.Platform), o.Platform) {
						valid = true
						break
					}
					if o.LocalPath != nil {
						local = o.LocalPath(folder, distro)
					}
				}
				if pathWithin(cwd, resolve(local, o.CWD, o.Platform), o.Platform) {
					valid = true
					break
				}
			}
		}
		if !valid && !includeInvalid {
			continue
		}
		if ancestry && p.Port != envPort {
			if !p.pidTruthy || !p.pidValid || !o.ProcessRunning(p.pid) {
				continue
			}
			if o.ParentPID != p.pid {
				if parents == nil {
					parents = map[int]bool{}
					if o.Ancestors != nil {
						ids, _ := o.Ancestors(ctx, o.ParentPID, 10)
						for _, id := range ids {
							parents[id] = true
						}
					}
				}
				if !parents[p.pid] {
					continue
				}
			}
		}
		p.Valid = valid
		if p.nameJSON == nil {
			p.Name = "IDE"
			if supported {
				if display, exists := displayNames[o.Terminal]; exists {
					p.Name = display
				} else if o.Terminal != "" {
					p.Name = strings.ToUpper(o.Terminal[:1]) + strings.ToLower(o.Terminal[1:])
				}
			}
		}
		host := s.host(ctx, p)
		if p.URL == "ws" {
			p.URL = "ws://" + host + ":" + numberText(p.Port)
		} else {
			p.URL = "http://" + host + ":" + numberText(p.Port) + "/sse"
		}
		result = append(result, p.IDE)
	}
	if !includeInvalid && envPort != 0 && !math.IsNaN(envPort) {
		matched := []IDE{}
		for _, ide := range result {
			if ide.Valid && ide.Port == envPort {
				matched = append(matched, ide)
			}
		}
		if len(matched) == 1 {
			return matched, nil
		}
	}
	return result, nil
}
func resolve(value, cwd, platform string) string {
	if platform == "windows" {
		return norm.NFC.String(nativepaths.ResolveWindows(value, cwd))
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(cwd, value)
	}
	return norm.NFC.String(filepath.Clean(value))
}
func pathWithin(cwd, path, platform string) bool {
	separator := "/"
	if platform == "windows" {
		separator = "\\"
		cwd = uppercaseDrive(cwd)
		path = uppercaseDrive(path)
	}
	return cwd == path || strings.HasPrefix(cwd, path+separator)
}
func uppercaseDrive(path string) string {
	if len(path) > 1 && path[1] == ':' {
		return strings.ToUpper(path[:1]) + path[1:]
	}
	return path
}
func DistroMatches(path, distro string) bool {
	for _, prefix := range []string{`\\wsl.localhost\`, `\\wsl$\`} {
		if strings.HasPrefix(path, prefix) {
			rest := strings.TrimPrefix(path, prefix)
			name, _, _ := strings.Cut(rest, `\`)
			return name == distro
		}
	}
	return true
}
func (s *Service) Find(ctx context.Context) (*IDE, error) {
	if e := s.Cleanup(ctx); e != nil && ctx.Err() != nil {
		return nil, e
	}
	start := s.options.Now()
	for s.options.Now().Sub(start) < 30*time.Second && ctx.Err() == nil {
		ides, e := s.Detect(ctx, false)
		if e != nil && ctx.Err() != nil {
			return nil, e
		}
		if len(ides) == 1 {
			result := ides[0]
			return &result, nil
		}
		if e = s.options.Sleep(ctx, time.Second); e != nil {
			return nil, e
		}
	}
	return nil, context.Cause(ctx)
}

func (s *Service) lockName(path string) string {
	if s.options.Platform == "windows" {
		path = strings.ReplaceAll(path, `\`, "/")
	}
	return filepath.Base(path)
}
