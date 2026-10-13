package ideintegration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func NewNative(configDir, cwd, platform string, environment map[string]string, parentPID int) *Service {
	env := map[string]string{}
	for k, v := range environment {
		env[k] = v
	}
	if platform == "linux" && env["WSL_DISTRO_NAME"] != "" {
		platform = "wsl"
	}
	run := func(ctx context.Context, name string, args []string, timeout time.Duration) (string, error) {
		active, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		executable, lookupError := nativeExecutable(name, cwd, env)
		if lookupError != nil {
			return "", lookupError
		}
		command := exec.CommandContext(active, executable, args...)
		command.Dir = cwd
		command.Env = []string{}
		keys := []string{}
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			command.Env = append(command.Env, k+"="+env[k])
		}
		command.WaitDelay = 250 * time.Millisecond
		configureProcess(command)
		var out bytes.Buffer
		command.Stdout = &out
		command.Stderr = &bytes.Buffer{}
		if e := command.Run(); e != nil {
			return "", e
		}
		return string(protocol.DecodeJavaScriptUTF8(out.Bytes())), nil
	}
	local := func(path, distro string) string {
		if !DistroMatches(path, distro) {
			return path
		}
		if text, e := run(context.Background(), "wslpath", []string{"-u", path}, 3*time.Second); e == nil {
			return protocol.TrimJavaScriptString(text)
		}
		path = strings.ReplaceAll(path, `\`, "/")
		if len(path) > 1 && path[1] == ':' {
			path = "/mnt/" + strings.ToLower(path[:1]) + path[2:]
		}
		return path
	}
	probe := func(ctx context.Context, host string, port float64) bool {
		if math.IsNaN(port) || port < 0 || port > 65535 {
			return false
		}
		dial := net.Dialer{Timeout: 500 * time.Millisecond}
		conn, e := dial.DialContext(ctx, "tcp", net.JoinHostPort(host, numberText(port)))
		if e != nil {
			return false
		}
		_ = conn.Close()
		return true
	}
	hostCache := map[string]string{}
	var hostMu sync.Mutex
	host := func(ctx context.Context, inWindows bool, port float64) string {
		if override := env["CLAUDE_CODE_IDE_HOST_OVERRIDE"]; override != "" {
			return override
		}
		if platform != "wsl" || !inWindows {
			return "127.0.0.1"
		}
		key := numberText(port)
		hostMu.Lock()
		if cached := hostCache[key]; cached != "" {
			hostMu.Unlock()
			return cached
		}
		hostMu.Unlock()
		value := "127.0.0.1"
		if text, e := run(ctx, "sh", []string{"-c", "ip route show | grep -i default"}, 3*time.Second); e == nil {
			match := regexp.MustCompile(`default via (\d+\.\d+\.\d+\.\d+)`).FindStringSubmatch(text)
			if len(match) > 1 && probe(ctx, match[1], port) {
				value = match[1]
			}
		}
		hostMu.Lock()
		hostCache[key] = value
		hostMu.Unlock()
		return value
	}
	paths := func(ctx context.Context) []string {
		result := []string{filepath.Join(configDir, "ide")}
		if platform != "wsl" {
			return result
		}
		profile := env["USERPROFILE"]
		if profile == "" {
			if text, e := run(ctx, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", "$env:USERPROFILE"}, 3*time.Second); e == nil {
				profile = protocol.TrimJavaScriptString(text)
			}
		}
		if profile != "" {
			result = append(result, filepath.Join(local(profile, env["WSL_DISTRO_NAME"]), ".claude", "ide"))
		}
		entries, e := os.ReadDir("/mnt/c/Users")
		if e == nil {
			for _, entry := range entries {
				if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
					continue
				}
				switch entry.Name() {
				case "Public", "Default", "Default User", "All Users":
					continue
				}
				result = append(result, filepath.Join("/mnt/c/Users", entry.Name(), ".claude", "ide"))
			}
		}
		return result
	}
	return New(Options{CWD: cwd, Platform: platform, Terminal: Terminal(env), Environment: env, ParentPID: parentPID, LocalPath: local, Host: host, Probe: probe, ProcessRunning: nativeProcessRunning, Remove: func(path string) error {
		if info, e := os.Lstat(path); e == nil && info.IsDir() {
			return errors.New("unlink: target is a directory")
		}
		return os.Remove(path)
	}, ReadLockfiles: func(ctx context.Context) ([]Lockfile, error) {
		result := []Lockfile{}
		for _, directory := range paths(ctx) {
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			entries, e := os.ReadDir(directory)
			if e != nil {
				continue
			}
			for _, entry := range entries {
				if !strings.HasSuffix(entry.Name(), ".lock") {
					continue
				}
				path := filepath.Join(directory, entry.Name())
				info, e := os.Stat(path)
				if e != nil {
					continue
				}
				data, readError := os.ReadFile(path)
				result = append(result, Lockfile{Path: path, Data: data, ReadError: readError, Modified: math.Trunc(float64(info.ModTime().Unix())*1000 + float64(info.ModTime().Nanosecond())/1e6)})
			}
		}
		return result, nil
	}, Ancestors: func(ctx context.Context, pid, max int) ([]int, error) {
		var name, script string
		var args []string
		if platform == "windows" {
			name = "powershell.exe"
			script = fmt.Sprintf("$pid = %d\n$ancestors = @()\nfor ($i = 0; $i -lt %d; $i++) {\n $proc = Get-CimInstance Win32_Process -Filter \"ProcessId=$pid\" -ErrorAction SilentlyContinue\n if (-not $proc -or -not $proc.ParentProcessId -or $proc.ParentProcessId -eq 0) { break }\n $pid = $proc.ParentProcessId\n $ancestors += $pid\n}\n$ancestors -join ','", pid, max)
			args = []string{"-NoProfile", "-Command", script}
		} else {
			name = "sh"
			script = fmt.Sprintf("pid=%d; for i in $(seq 1 %d); do ppid=$(ps -o ppid= -p $pid 2>/dev/null | tr -d ' '); if [ -z \"$ppid\" ] || [ \"$ppid\" = \"0\" ] || [ \"$ppid\" = \"1\" ]; then break; fi; echo $ppid; pid=$ppid; done", pid, max)
			args = []string{"-c", script}
		}
		text, e := run(ctx, name, args, 3*time.Second)
		if e != nil {
			return nil, e
		}
		ids := []int{}
		for _, row := range strings.FieldsFunc(protocol.TrimJavaScriptString(text), func(r rune) bool { return r == ',' || r == '\n' }) {
			n, e := strconv.Atoi(row)
			if e == nil {
				ids = append(ids, n)
			}
		}
		return ids, nil
	}})
}

// exec.Cmd's LookPath reads the host process's PATH before Cmd.Env is set.
// Resolve from the same immutable environment used by the actual child.
func nativeExecutable(name, cwd string, env map[string]string) (string, error) {
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) {
		if !filepath.IsAbs(name) {
			name = filepath.Join(cwd, name)
		}
		return name, nil
	}
	path, defined := env["PATH"]
	if !defined {
		path = "/usr/bin:/bin"
	}
	for _, directory := range filepath.SplitList(path) {
		if directory == "" {
			directory = cwd
		} else if !filepath.IsAbs(directory) {
			directory = filepath.Join(cwd, directory)
		}
		candidate := filepath.Join(directory, name)
		if info, e := os.Stat(candidate); e == nil && !info.IsDir() && nativeExecutableMode(info.Mode()) {
			return candidate, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
