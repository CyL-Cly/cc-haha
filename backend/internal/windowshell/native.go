package windowshell

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
)

func NewNative(platform, cwd string, environment map[string]string) *Resolver {
	env := make(map[string]string, len(environment))
	for name, value := range environment {
		env[name] = value
	}
	where := func(ctx context.Context, command string) (string, error) {
		root := environmentValue(env, "SystemRoot")
		if root == "" {
			root = environmentValue(env, "windir")
		}
		executable := nativepaths.JoinWindows(root, "System32", "where.exe")
		if root == "" {
			executable = whichNative(platform, "where.exe", env)
		}
		data, err := runProbe(ctx, executable, []string{command}, cwd, env, 1024*1024)
		return strings.TrimSpace(string(data)), err
	}
	return New(platform, cwd, env, Dependencies{PathExists: func(ctx context.Context, path string) bool {
		if ctx.Err() != nil {
			return false
		}
		_, err := os.Stat(path)
		return err == nil
	}, FileExists: func(ctx context.Context, path string) bool {
		if ctx.Err() != nil {
			return false
		}
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	}, Where: where, Which: func(ctx context.Context, name string) string {
		if ctx.Err() != nil {
			return ""
		}
		return whichNative(platform, name, env)
	}, ReadWindowsPwshHome: func(ctx context.Context) string {
		root := environmentValue(env, "SystemRoot")
		if root == "" {
			root = environmentValue(env, "windir")
		}
		executable := nativepaths.JoinWindows(root, "System32", "cmd.exe")
		if root == "" {
			executable = whichNative(platform, "cmd.exe", env)
		}
		data, err := runProbe(ctx, executable, []string{"/C", "pwsh", "-NoProfile", "-Command", "$PSHOME"}, cwd, env, 8192)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}})
}

func environmentValue(env map[string]string, name string) string {
	if value, ok := env[name]; ok {
		return value
	}
	for key, value := range env {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func whichNative(platform, name string, environment map[string]string) string {
	if filepath.IsAbs(name) || IsWindows(platform) && nativepaths.IsWindowsAbsolute(name) {
		if info, err := os.Stat(name); err == nil && !info.IsDir() && (IsWindows(platform) || info.Mode()&0111 != 0) {
			return name
		}
		return ""
	}
	directories := filepath.SplitList(environment["PATH"])
	extensions := []string{""}
	if IsWindows(platform) {
		directories = strings.Split(environmentValue(environment, "PATH"), ";")
		if filepath.Ext(name) == "" {
			extensions = strings.Split(environmentValue(environment, "PATHEXT"), ";")
			if len(extensions) == 0 || len(extensions) == 1 && extensions[0] == "" {
				extensions = []string{".COM", ".EXE", ".BAT", ".CMD"}
			}
		}
	}
	for _, directory := range directories {
		if directory == "" {
			continue
		}
		for _, extension := range extensions {
			candidate := filepath.Join(directory, name+extension)
			if IsWindows(platform) {
				candidate = nativepaths.JoinWindows(directory, name+extension)
			}
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && (IsWindows(platform) || info.Mode()&0111 != 0) {
				return candidate
			}
		}
	}
	return ""
}

type boundedProbeBuffer struct {
	data    bytes.Buffer
	maximum int
}

func (b *boundedProbeBuffer) Write(value []byte) (int, error) {
	if len(value) > b.maximum-b.data.Len() {
		return 0, errors.New("native shell discovery exceeded its Source output buffer")
	}
	return b.data.Write(value)
}

func runProbe(ctx context.Context, executable string, args []string, cwd string, environment map[string]string, maximum int) ([]byte, error) {
	if executable == "" {
		return nil, exec.ErrNotFound
	}
	timed, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	command := exec.CommandContext(timed, executable, args...)
	command.Dir = cwd
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	command.Env = make([]string, 0, len(keys))
	for _, key := range keys {
		command.Env = append(command.Env, key+"="+environment[key])
	}
	configureProbeProcess(command)
	stdout := &boundedProbeBuffer{maximum: maximum}
	stderr := &boundedProbeBuffer{maximum: maximum}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		return nil, err
	}
	return append([]byte(nil), stdout.data.Bytes()...), nil
}
