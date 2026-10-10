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
	config := env["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = filepath.Join(home, ".claude")
	}
	config, err = filepath.Abs(config)
	if err != nil {
		return nil, err
	}
	env["HOME"], env["CLAUDE_CONFIG_DIR"] = home, config
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
	n := &NativeBoundary{Home: home, CWD: cwd, Temp: os.TempDir(), ManagedRoot: managed, Environment: env, Now: now, NowMillis: func() float64 { return float64(now().UnixMilli()) }, NativeProcess: true, ConfigDirExplicit: o.Environment["CLAUDE_CONFIG_DIR"] != ""}
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
	// The remote cache is eligible only for first-party Anthropic credentials.
	// DeepSeek/custom origins must never inherit an unrelated Anthropic cache.
	base := n.Environment["ANTHROPIC_BASE_URL"]
	u, _ := url.Parse(base)
	firstParty := base == "" || u != nil && u.Hostname() == "api.anthropic.com"
	for _, k := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if envTruthyNative(n.Environment[k]) {
			firstParty = false
		}
	}
	if firstParty && n.Environment["CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH"] != "1" && n.Environment["CLAUDE_CODE_ENTRYPOINT"] != "local-agent" {
		if n.Environment["ANTHROPIC_API_KEY"] != "" || n.Environment["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
			p := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "remote-settings.json")
			if raw, err := n.readFile(p); err == nil {
				var object protocol.JSONObject
				if object.UnmarshalJSON(raw) == nil && object.IsObject() && len(object.OwnProperties()) > 0 {
					return nativeSettings(raw, p)
				}
			}
			// Remote prefetch has not been ported; never claim a cache miss is
			// proof that the enterprise policy is absent.
			return nil, mainCap("first-party remote managed policy prefetch")
		}
	}
	admin, userPolicy, err := readMDM()
	if err != nil {
		return nil, err
	}
	if admin != nil && len(admin.OwnProperties()) > 0 {
		return admin, nil
	}
	layers := []*protocol.JSONObject{}
	basePath := filepath.Join(n.ManagedRoot, "managed-settings.json")
	if raw, err := n.readFile(basePath); err == nil {
		parsed, err := nativeSettings(raw, basePath)
		if err != nil {
			return nil, err
		}
		layers = append(layers, parsed)
	} else if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission) {
		return nil, err
	}
	dropRoot := filepath.Join(n.ManagedRoot, "managed-settings.d")
	entries, err := n.readDir(dropRoot)
	if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission) {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") || !(entry.Type().IsRegular() || entry.Type()&fs.ModeSymlink != 0) {
			continue
		}
		p := filepath.Join(dropRoot, name)
		raw, err := n.readFile(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				continue
			}
			return nil, err
		}
		parsed, err := nativeSettings(raw, p)
		if err != nil {
			return nil, err
		}
		layers = append(layers, parsed)
	}
	merged, err := clistartupenv.MergePolicyLayers(layers...)
	if err != nil || merged != nil {
		return merged, err
	}
	return userPolicy, nil
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
	root := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "plugins")
	entries, err := n.readDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}
