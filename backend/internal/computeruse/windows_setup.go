package computeruse

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// These are the same runtime assets embedded by the original server.
//
//go:embed assets/requirements-win.txt
var windowsRequirements []byte

//go:embed assets/win_helper.py
var windowsHelper []byte

//go:embed assets/win_cursor_badge.py
var windowsCursorBadge []byte

type pythonRuntime struct {
	Installed bool    `json:"installed"`
	Version   *string `json:"version"`
	Path      *string `json:"path"`
	Source    *string `json:"source"`
	Error     *string `json:"error"`
	command   string
	prefix    []string
}

var pythonVersion = regexp.MustCompile(`(?i)Python\s+([0-9][^\s]*)`)
var pythonFloor = regexp.MustCompile(`^(\d+)\.(\d+)`)

func textPointer(v string) *string { return &v }
func versionFromOutput(v string) *string {
	if m := pythonVersion.FindStringSubmatch(v); len(m) > 1 {
		return textPointer(m[1])
	}
	return nil
}
func pythonSupported(v *string) bool {
	if v == nil {
		return false
	}
	m := pythonFloor.FindStringSubmatch(*v)
	if len(m) != 3 {
		return false
	}
	major, _ := strconv.ParseFloat(m[1], 64)
	minor, _ := strconv.ParseFloat(m[2], 64)
	return major > 3 || major == 3 && minor >= 9
}
func (s *Service) pythonCommand(ctx context.Context, cmd string, args []string) CommandResult {
	r, err := s.run(ctx, cmd, args, nil)
	if err != nil && r.Code == 0 {
		r.Code = -1
	}
	if r.Code == -1 && r.Stderr == "" {
		r.Stderr = "Failed to run " + cmd
	}
	r.Stdout = trim(r.Stdout)
	r.Stderr = trim(r.Stderr)
	return r
}
func (s *Service) detectWindowsPython(ctx context.Context, venv, custom string) pythonRuntime {
	custom = trim(custom)
	if custom != "" {
		r := s.pythonCommand(ctx, custom, []string{"--version"})
		p := pythonRuntime{Installed: r.Code == 0, Path: textPointer(custom), Source: textPointer("custom"), command: custom, prefix: []string{}}
		if p.Installed {
			p.Version = versionFromOutput(r.Stdout + "\n" + r.Stderr)
		} else {
			message := r.Stderr
			if message == "" {
				message = r.Stdout
			}
			if message == "" {
				message = "Failed to run " + custom
			}
			p.Error = textPointer(message)
		}
		return p
	}
	for _, c := range []struct {
		cmd    string
		prefix []string
	}{{"python3", []string{}}, {"python", []string{}}, {"py", []string{"-3"}}, {"py", []string{}}} {
		r := s.pythonCommand(ctx, c.cmd, append(append([]string{}, c.prefix...), "--version"))
		if r.Code != 0 {
			continue
		}
		p := pythonRuntime{Installed: true, Version: versionFromOutput(r.Stdout + "\n" + r.Stderr), Source: textPointer("system"), command: c.cmd, prefix: c.prefix}
		locator := s.pythonCommand(ctx, "where", []string{c.cmd})
		if locator.Code == 0 {
			for _, line := range strings.Split(locator.Stdout, "\n") {
				if trim(line) != "" {
					p.Path = textPointer(trim(line))
					break
				}
			}
		}
		return p
	}
	if venv != "" {
		r := s.pythonCommand(ctx, venv, []string{"--version"})
		if r.Code == 0 {
			return pythonRuntime{Installed: true, Version: versionFromOutput(r.Stdout + "\n" + r.Stderr), Path: textPointer(venv), Source: textPointer("venv"), command: venv, prefix: []string{}}
		}
	}
	return pythonRuntime{prefix: []string{}}
}
func (s *Service) windowsRuntimeRoot() string { return filepath.Join(s.options.ConfigDir, ".runtime") }
func (s *Service) windowsPythonPath() string {
	return filepath.Join(s.windowsRuntimeRoot(), "venv", "Scripts", "python.exe")
}
func (s *Service) windowsBaseMatches(custom string) bool {
	data, err := os.ReadFile(filepath.Join(s.windowsRuntimeRoot(), "venv-base-interpreter.txt"))
	if err != nil {
		return trim(custom) == ""
	}
	return trim(string(data)) == trim(custom)
}
func (s *Service) writeWindowsRuntime() error {
	r := s.windowsRuntimeRoot()
	if err := os.MkdirAll(r, 0700); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{"requirements.txt", windowsRequirements}, {"win_helper.py", windowsHelper}, {"win_cursor_badge.py", windowsCursorBadge}} {
		if err := os.WriteFile(filepath.Join(r, f.name), f.data, 0600); err != nil {
			return err
		}
	}
	return nil
}
func requirementsDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (s *Service) pipInstall(ctx context.Context, python string, args []string) CommandResult {
	mirror := append(append([]string{}, args...), "-i", "https://pypi.tuna.tsinghua.edu.cn/simple/", "--trusted-host", "pypi.tuna.tsinghua.edu.cn")
	first := s.pythonCommand(ctx, python, mirror)
	if first.Code == 0 {
		return first
	}
	second := s.pythonCommand(ctx, python, args)
	if second.Code == 0 {
		return second
	}
	return first
}

type setupStep struct {
	Name         string `json:"name"`
	OK           bool   `json:"ok"`
	Message      string `json:"message"`
	messageUTF16 []uint16
}

func (s setupStep) MarshalJSON() ([]byte, error) {
	message := encode(s.Message)
	if s.messageUTF16 != nil {
		message = protocol.QuoteJavaScriptUTF16(s.messageUTF16)
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: encode(s.Name)}, protocol.JSONField{Name: "ok", Value: encode(s.OK)}, protocol.JSONField{Name: "message", Value: message}).MarshalJSON()
}

type setupResult struct {
	Success bool        `json:"success"`
	Steps   []setupStep `json:"steps"`
}

// WindowsSetup uses the original marker/stamp format, so existing runtimes
// survive migration and a changed custom interpreter rebuilds only that venv.
func (s *Service) WindowsSetup(ctx context.Context) (setupResult, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return setupResult{Steps: []setupStep{}}, context.Canceled
	}
	s.operations.Add(1)
	s.mu.Unlock()
	defer s.operations.Done()
	ctx, cancel := s.ownedContext(ctx)
	defer cancel()
	out := setupResult{Steps: []setupStep{}}
	add := func(name string, ok bool, msg string) {
		out.Steps = append(out.Steps, setupStep{Name: name, OK: ok, Message: msg})
	}
	config, err := s.Config(ctx)
	if err != nil {
		return out, err
	}
	custom, _ := stringValue(config["pythonPath"])
	r := s.windowsRuntimeRoot()
	venv := filepath.Join(r, "venv")
	python := s.windowsPythonPath()
	created := exists(python)
	if created && !s.windowsBaseMatches(custom) {
		err := os.RemoveAll(venv)
		for _, name := range []string{"requirements.sha256", "venv-base-interpreter.txt"} {
			if err == nil {
				err = os.Remove(filepath.Join(r, name))
				if os.IsNotExist(err) {
					err = nil
				}
			}
		}
		if err != nil {
			add("venv_rebuild", false, "移除旧虚拟环境失败: "+err.Error())
			return out, nil
		}
		created = false
		add("venv_rebuild", true, "检测到自定义解释器变更，已移除旧虚拟环境以便重建")
	}
	prior := ""
	if created {
		prior = python
	}
	p := s.detectWindowsPython(ctx, prior, custom)
	if !p.Installed {
		msg := "Python 3 未安装，请先安装 Python 3"
		if p.Source != nil && *p.Source == "custom" {
			reason := custom
			if p.Error != nil {
				reason = *p.Error
			}
			msg = "自定义 Python 路径不可用: " + reason
		}
		add("python_check", false, msg)
		return out, nil
	}
	version := "null"
	if p.Version != nil {
		version = *p.Version
	}
	message := "Python " + version
	if p.Source != nil && *p.Source == "custom" {
		message += "（使用自定义解释器）"
	} else if p.Source != nil && *p.Source == "venv" {
		message += "（使用现有虚拟环境）"
	}
	add("python_check", true, message)
	if !pythonSupported(p.Version) {
		version = "unknown"
		if p.Version != nil {
			version = *p.Version
		}
		add("python_version", false, "Computer Use 需要 Python >= 3.9，当前版本为 "+version)
		return out, nil
	}
	if err = s.writeWindowsRuntime(); err != nil {
		add("runtime_files", false, "提取运行时文件失败: "+err.Error())
		return out, nil
	}
	add("runtime_files", true, "运行时文件已就绪")
	if !created {
		if p.command == "" {
			add("venv", false, "未找到可用于创建虚拟环境的 Python 命令")
			return out, nil
		}
		result := s.pythonCommand(ctx, p.command, append(append([]string{}, p.prefix...), "-m", "venv", venv))
		if result.Code != 0 {
			add("venv", false, "创建虚拟环境失败: "+result.Stderr)
			return out, nil
		}
		if err = os.WriteFile(filepath.Join(r, "venv-base-interpreter.txt"), []byte(custom), 0600); err != nil {
			add("venv", false, "写入虚拟环境标记文件失败: "+err.Error())
			return out, nil
		}
		add("venv", true, "虚拟环境已创建")
	} else {
		add("venv", true, "虚拟环境已存在")
	}
	if !exists(filepath.Join(venv, "Scripts", "pip.exe")) {
		result := s.pythonCommand(ctx, python, []string{"-m", "ensurepip", "--upgrade"})
		if result.Code != 0 {
			add("pip", false, "安装 pip 失败: "+result.Stderr)
			return out, nil
		}
	}
	add("pip", true, "pip 已就绪")
	data, err := os.ReadFile(filepath.Join(r, "requirements.txt"))
	if err != nil {
		return out, err
	}
	digest := requirementsDigest(data)
	stamp, _ := os.ReadFile(filepath.Join(r, "requirements.sha256"))
	if trim(string(stamp)) != digest {
		_ = s.pipInstall(ctx, python, []string{"-m", "pip", "install", "--upgrade", "pip"})
		result := s.pipInstall(ctx, python, []string{"-m", "pip", "install", "-r", filepath.Join(r, "requirements.txt")})
		if result.Code != 0 {
			message := utf16.Encode([]rune(result.Stderr))
			if len(message) > 500 {
				message = message[:500]
			}
			units := append(utf16.Encode([]rune("安装依赖失败: ")), message...)
			add("deps", false, string(utf16.Decode(units)))
			out.Steps[len(out.Steps)-1].messageUTF16 = units
			return out, nil
		}
		if err = os.WriteFile(filepath.Join(r, "requirements.sha256"), []byte(digest+"\n"), 0600); err != nil {
			return out, err
		}
		add("deps", true, "依赖已安装")
	} else {
		add("deps", true, "依赖已是最新")
	}
	out.Success = true
	return out, nil
}

func (s *Service) windowsStatus(ctx context.Context, capability object) (object, error) {
	config, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	custom, _ := stringValue(config["pythonPath"])
	python := s.windowsPythonPath()
	created := exists(python)
	prior := ""
	if created {
		prior = python
	}
	p := s.detectWindowsPython(ctx, prior, custom)
	effective := created && s.windowsBaseMatches(custom)
	r := s.windowsRuntimeRoot()
	installed := false
	if effective {
		data, e := os.ReadFile(filepath.Join(r, "requirements.txt"))
		stamp, se := os.ReadFile(filepath.Join(r, "requirements.sha256"))
		installed = e == nil && se == nil && trim(string(stamp)) == requirementsDigest(data)
	}
	perms := Permissions{}
	if effective && installed {
		_ = s.writeWindowsRuntime()
		helper := filepath.Join(r, "win_helper.py")
		if exists(helper) {
			res := s.pythonCommand(ctx, python, []string{helper, "check_permissions"})
			if res.Code == 0 {
				var envelope struct {
					OK     bool            `json:"ok"`
					Result json.RawMessage `json:"result"`
				}
				if json.Unmarshal([]byte(res.Stdout), &envelope) == nil && envelope.OK && len(envelope.Result) > 0 && string(envelope.Result) != "null" {
					var decoded Permissions
					if json.Unmarshal(envelope.Result, &decoded) == nil {
						perms = decoded
						perms.Error = nil
					}
				}
			}
		}
	}
	capability["platform"] = encode(s.options.Platform)
	capability["arch"] = encode(s.options.Arch)
	capability["systemVersion"] = encode(nil)
	capability["python"] = encode(p)
	capability["venv"] = encode(map[string]any{"created": effective, "path": filepath.Join(r, "venv")})
	capability["dependencies"] = encode(map[string]any{"installed": installed, "requirementsFound": true})
	capability["permissions"] = encode(perms)
	return capability, nil
}
