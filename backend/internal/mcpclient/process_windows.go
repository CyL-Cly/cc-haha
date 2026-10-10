//go:build windows

package mcpclient

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func configureMCPProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
func windowsEnv(env map[string]string, key string) string {
	if value, ok := env[key]; ok {
		return value
	}
	for name, value := range env {
		if strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}
func lookupWindowsCommand(name string, env map[string]string, cwd string) (string, error) {
	dirs := append([]string{cwd}, filepath.SplitList(windowsEnv(env, "PATH"))...)
	if strings.ContainsAny(name, "/\\") {
		dirs = []string{""}
	}
	extensions := strings.Split(windowsEnv(env, "PATHEXT"), ";")
	if len(extensions) == 1 && extensions[0] == "" {
		extensions = []string{".COM", ".EXE", ".BAT", ".CMD"}
	}
	if filepath.Ext(name) != "" {
		extensions = append([]string{""}, extensions...)
	}
	for _, dir := range dirs {
		candidate := name
		if dir != "" {
			candidate = filepath.Join(dir, name)
		}
		if !filepath.IsAbs(candidate) && cwd != "" {
			candidate = filepath.Join(cwd, candidate)
		}
		for _, extension := range extensions {
			path := candidate + extension
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, nil
			}
		}
	}
	// cross-spawn repeats the lookup without PATHEXT for shebang scripts.
	for _, dir := range dirs {
		candidate := name
		if dir != "" {
			candidate = filepath.Join(dir, name)
		}
		if !filepath.IsAbs(candidate) && cwd != "" {
			candidate = filepath.Join(cwd, candidate)
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("spawn %s ENOENT", name)
}
func prepareMCPCommand(cmd *exec.Cmd, env map[string]string, cwd string) error {
	command := cmd.Args[0]
	args := append([]string(nil), cmd.Args[1:]...)
	file, err := lookupWindowsCommand(command, env, cwd)
	if err != nil {
		return err
	}
	// Windows needs explicit interpreter support for npm's script executables.
	if f, e := os.Open(file); e == nil {
		buffer := make([]byte, 150)
		n, _ := f.Read(buffer)
		_ = f.Close()
		line := strings.SplitN(string(buffer[:n]), "\n", 2)[0]
		if strings.HasPrefix(line, "#!") {
			line = strings.TrimPrefix(line, "#!")
			line = strings.TrimPrefix(line, " ")
			parts := strings.Split(line, " ")
			binary := filepath.Base(strings.ReplaceAll(parts[0], "/", "\\"))
			if binary == "env" && len(parts) > 1 {
				command = parts[1]
			} else {
				command = binary
				if len(parts) > 1 && parts[1] != "" {
					command += " " + parts[1]
				}
			}
			args = append([]string{file}, args...)
			resolved, e := lookupWindowsCommand(command, env, cwd)
			if e == nil {
				file = resolved
			} else {
				file = ""
			}
		}
	}
	extension := strings.ToLower(filepath.Ext(file))
	if extension == ".exe" || extension == ".com" {
		cmd.Path = file
		cmd.Args = append([]string{command}, args...)
		cmd.Err = nil
		return nil
	}
	double := strings.HasSuffix(strings.ToLower(file), ".cmd") && strings.Contains(strings.ToLower(strings.ReplaceAll(file, "/", "\\")), `node_modules\.bin\`)
	parts := []string{escapeWindowsMeta(filepath.Clean(command))}
	for _, arg := range args {
		parts = append(parts, escapeWindowsArgument(arg, double))
	}
	shell := windowsEnv(env, "COMSPEC")
	if shell == "" {
		shell = "cmd.exe"
	}
	resolved, err := lookupWindowsCommand(shell, env, cwd)
	if err != nil {
		return err
	}
	cmd.Path = resolved
	cmd.Args = []string{shell, "/d", "/s", "/c", "\"" + strings.Join(parts, " ") + "\""}
	cmd.Err = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CmdLine: syscall.EscapeArg(shell) + " /d /s /c " + cmd.Args[4]}
	return nil
}
func signalMCPProcess(cmd *exec.Cmd, level int) error {
	args := []string{"/PID", strconv.Itoa(cmd.Process.Pid), "/T"}
	if level == 2 {
		args = append(args, "/F")
	}
	kill := exec.Command("taskkill", args...)
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := kill.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
