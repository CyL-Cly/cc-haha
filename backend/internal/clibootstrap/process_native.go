package clibootstrap

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
)

type NativeProcessOptions struct {
	Environment   map[string]string
	CWD, Platform string
	// Native primitives are replaceable for deterministic tests. Production
	// supplies none of these overrides and reads the platform's actual sources.
	ManagedRoot string
	ReadMDM     func() (admin, user *protocol.JSONObject, err error)
}

func NewProcessNativeBoundary(o NativeProcessOptions) (*NativeBoundary, error) {
	env := make(map[string]string, len(o.Environment)+2)
	for k, v := range o.Environment {
		env[k] = v
	}
	home := env["HOME"]
	if o.Platform == "windows" && env["USERPROFILE"] != "" {
		home = env["USERPROFILE"]
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	cwd := o.CWD
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	// A spawned Source CLI observes process.cwd(), which resolves the
	// directory used for chdir. Retaining a /var or user symlink alias here
	// gives the same workspace two transcript/project IDs after the server
	// has already canonicalized its launch directory.
	if resolved, resolveErr := filepath.EvalSymlinks(cwd); resolveErr == nil {
		cwd = resolved
	}
	// Source retains the launch path when realpath fails (for example a File
	// Provider mount), and normalizes either result before creating state.
	cwd = norm.NFC.String(cwd)
	if info, err := os.Stat(cwd); err != nil {
		return nil, err
	} else if !info.IsDir() {
		return nil, errors.New("working directory is not a directory")
	}
	// process.env retains the launch bytes, including an absent or explicitly
	// empty directory. Filesystem consumers resolve their own paths against
	// the child process CWD rather than rewriting that environment authority.
	managed := o.ManagedRoot
	if managed == "" {
		switch o.Platform {
		case "darwin":
			managed = "/Library/Application Support/ClaudeCode"
		case "windows":
			managed = `C:\Program Files\ClaudeCode`
		default:
			managed = "/etc/claude-code"
		}
		if env["USER_TYPE"] == "ant" && env["CLAUDE_CODE_MANAGED_SETTINGS_PATH"] != "" {
			managed = env["CLAUDE_CODE_MANAGED_SETTINGS_PATH"]
		}
	}
	now := time.Now
	n := &NativeBoundary{Home: home, CWD: cwd, Temp: os.TempDir(), ManagedRoot: managed, Environment: env, Now: now, NowMillis: func() float64 { return float64(now().UnixMilli()) }, NativeProcess: true, ConfigDirExplicit: o.Environment["CLAUDE_CONFIG_DIR"] != "", platform: o.Platform}
	readMDM := o.ReadMDM
	n.mdm = &nativeMDMCache{read: func(ctx context.Context) (*protocol.JSONObject, *protocol.JSONObject, error) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if readMDM != nil {
			return readMDM()
		}
		return readProcessMDMContext(ctx, o.Platform, home, env)
	}}
	n.ResolvePolicy = func() (*protocol.JSONObject, error) {
		return n.resolveNativePolicy(func() (*protocol.JSONObject, *protocol.JSONObject, error) { return n.mdm.current(context.Background()) })
	}
	if err := n.TransportOrigin(); err != nil {
		return nil, err
	}
	return n, nil
}

func nativeSettings(raw []byte, path string) (*protocol.JSONObject, error) {
	parsed, err := clisettings.ValidateNativeJSONContent(strings.TrimPrefix(string(raw), "\ufeff"), path)
	if err != nil {
		return nil, err
	}
	return parsed.Settings, nil
}

func (n *NativeBoundary) resolveNativePolicy(readMDM func() (*protocol.JSONObject, *protocol.JSONObject, error)) (*protocol.JSONObject, error) {
	observation, err := n.resolveNativePolicyObserved(readMDM)
	if err == nil {
		n.mu.Lock()
		n.policyObservation = &observation
		n.mu.Unlock()
	}
	return observation.Settings, err
}
func (n *NativeBoundary) resolveNativePolicyObserved(readMDM func() (*protocol.JSONObject, *protocol.JSONObject, error)) (observation NativePolicyObservation, err error) {
	environment := n.nativeEnvironmentSnapshot()
	observe := func(value *protocol.JSONObject, origin string, hasBase, hasDropIns bool, e error) (NativePolicyObservation, error) {
		return NativePolicyObservation{Settings: value, Origin: origin, HasBase: hasBase, HasDropIns: hasDropIns}, e
	}
	// The remote cache is eligible only for first-party Anthropic credentials.
	// DeepSeek/custom origins must never inherit an unrelated Anthropic cache.
	base := environment["ANTHROPIC_BASE_URL"]
	u, _ := url.Parse(base)
	firstParty := base == "" || u != nil && u.Hostname() == "api.anthropic.com"
	for _, k := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if envTruthyNative(environment[k]) {
			firstParty = false
		}
	}
	n.mu.Lock()
	remote := n.remoteManaged
	n.mu.Unlock()
	if remote != nil {
		if value := remote.snapshot(); value != nil && len(value.OwnProperties()) > 0 {
			return observe(value, "remote", false, false, nil)
		}
	} else if firstParty && environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] != "1" && environment["CLAUDE_CODE_ENTRYPOINT"] != "local-agent" {
		if environment["ANTHROPIC_API_KEY"] != "" || environment["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
			p := filepath.Join(n.ConfigHomeDirectory(), "remote-settings.json")
			if raw, err := n.readFile(p); err == nil {
				var object protocol.JSONObject
				if object.UnmarshalJSON(raw) == nil && object.IsObject() && len(object.OwnProperties()) > 0 {
					value, e := nativeSettings(raw, p)
					return observe(value, "remote", false, false, e)
				}
			}
			// A standalone finite resolver has no init authority. Only the
			// actual process factory's installed service can prove fail-open.
			return observation, mainCap("first-party remote managed policy prefetch")
		}
	}
	admin, userPolicy, err := readMDM()
	if err != nil {
		return observation, err
	}
	if admin != nil && len(admin.OwnProperties()) > 0 {
		origin := "plist"
		if n.platform == "windows" {
			origin = "hklm"
		}
		return observe(admin, origin, false, false, nil)
	}
	layers := []*protocol.JSONObject{}
	hasBase, hasDropIns := false, false
	basePath := filepath.Join(n.ManagedRoot, "managed-settings.json")
	if raw, err := n.readFile(basePath); err == nil {
		parsed, err := nativeSettings(raw, basePath)
		if err != nil {
			return observation, err
		}
		layers = append(layers, parsed)
		hasBase = hasBase || parsed != nil && len(parsed.OwnProperties()) > 0
	} else if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission) {
		return observation, err
	}
	dropRoot := filepath.Join(n.ManagedRoot, "managed-settings.d")
	entries, err := n.readDir(dropRoot)
	if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission) {
		return observation, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") || !(entry.Type().IsRegular() || entry.Type()&fs.ModeSymlink != 0) {
			continue
		}
		hasDropIns = true
		p := filepath.Join(dropRoot, name)
		raw, err := n.readFile(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				continue
			}
			return observation, err
		}
		parsed, err := nativeSettings(raw, p)
		if err != nil {
			return observation, err
		}
		layers = append(layers, parsed)
	}
	merged, err := clistartupenv.MergePolicyLayers(layers...)
	if err != nil || merged != nil {
		return observe(merged, "file", hasBase, hasDropIns, err)
	}
	origin := ""
	if userPolicy != nil && len(userPolicy.OwnProperties()) > 0 {
		origin = "hkcu"
	}
	return observe(userPolicy, origin, false, false, nil)
}

func envTruthyNative(v string) bool {
	switch strings.ToLower(strings.TrimFunc(v, func(r rune) bool { return r == '\ufeff' || r != '\u0085' && unicode.IsSpace(r) })) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

var nativeRegSettings = regexp.MustCompile(`(?i)^\s+Settings\s+REG_(?:EXPAND_)?SZ\s+(.+)$`)

func readProcessMDM(platform, home string, env map[string]string) (*protocol.JSONObject, *protocol.JSONObject, error) {
	return readProcessMDMContext(context.Background(), platform, home, env)
}

func readProcessMDMContext(parentContext context.Context, platform, home string, env map[string]string) (*protocol.JSONObject, *protocol.JSONObject, error) {
	run := func(exe string, args ...string) []byte {
		ctx, cancel := context.WithTimeout(parentContext, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, args...)
		out, err := cmd.Output()
		if err != nil {
			return nil
		}
		return out
	}
	if platform == "darwin" {
		paths := []string{}
		if u, err := user.Current(); err == nil && u.Username != "" {
			paths = append(paths, filepath.Join("/Library/Managed Preferences", u.Username, "com.anthropic.claudecode.plist"))
		}
		paths = append(paths, "/Library/Managed Preferences/com.anthropic.claudecode.plist")
		if env["USER_TYPE"] == "ant" {
			paths = append(paths, filepath.Join(home, "Library/Preferences/com.anthropic.claudecode.plist"))
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err != nil {
				continue
			}
			raw := run("/usr/bin/plutil", "-convert", "json", "-o", "-", "--", p)
			if len(raw) == 0 {
				continue
			}
			policy, err := nativeSettings(raw, p)
			return policy, nil, err
		}
	}
	if platform == "windows" {
		read := func(key string) (*protocol.JSONObject, error) {
			raw := run("reg", "query", key, "/v", "Settings")
			for _, line := range strings.Split(string(raw), "\n") {
				if m := nativeRegSettings.FindStringSubmatch(strings.TrimSuffix(line, "\r")); m != nil {
					return nativeSettings([]byte(strings.TrimRight(m[1], " \t")), "Registry: "+key+`\Settings`)
				}
			}
			return nil, nil
		}
		admin, err := read(`HKLM\SOFTWARE\Policies\ClaudeCode`)
		if err != nil {
			return nil, nil, err
		}
		userPolicy, err := read(`HKCU\SOFTWARE\Policies\ClaudeCode`)
		return admin, userPolicy, err
	}
	return nil, nil, nil
}

func (n *NativeBoundary) nativePluginsAbsent() (bool, error) {
	root := filepath.Join(n.ConfigHomeDirectory(), "plugins")
	entries, err := n.readDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}
