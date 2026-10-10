package clibootstrap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type nativeAgentRipgrep struct {
	Command string
	Args    []string
}

func nativeAgentProcessEnvironment(n *NativeBoundary) []string {
	keys := make([]string, 0, len(n.Environment))
	for key := range n.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := []string{}
	for _, key := range keys {
		env = append(env, key+"="+n.Environment[key])
	}
	return env
}

func nativeAgentRipgrepConfig(n *NativeBoundary, executable, vendorRoot string) (nativeAgentRipgrep, error) {
	usable := func(path string) bool {
		normalized := strings.ReplaceAll(path, `\`, "/")
		if strings.Contains(normalized, "$bunfs") || strings.Contains(normalized, "~BUN") {
			return false
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(n.CWD, path)
		}
		_, err := os.Stat(path)
		return err == nil
	}
	if !nativeEnvFalsy(n.Environment["USE_BUILTIN_RIPGREP"]) {
		if explicit := strings.Trim(n.Environment["CC_HAHA_RIPGREP_PATH"], " \t\n\r\v\f\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"); explicit != "" && usable(explicit) {
			return nativeAgentRipgrep{explicit, []string{"--no-config"}}, nil
		}
		name := "rg"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		sibling := filepath.Join(filepath.Dir(executable), name)
		if executable != "" && usable(sibling) {
			return nativeAgentRipgrep{sibling, []string{"--no-config"}}, nil
		}
		arch := runtime.GOARCH
		if arch == "amd64" {
			arch = "x64"
		}
		platform := runtime.GOOS
		if platform == "windows" {
			platform = "win32"
		}
		vendor := filepath.Join(vendorRoot, arch+"-"+platform, name)
		if vendorRoot != "" && usable(vendor) {
			return nativeAgentRipgrep{vendor, []string{}}, nil
		}
	}
	seen := map[string]bool{}
	for _, directory := range filepath.SplitList(n.Environment["PATH"]) {
		if directory == "" {
			continue
		}
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(n.CWD, directory)
		}
		extensions := []string{""}
		if runtime.GOOS == "windows" {
			pathext := n.Environment["PATHEXT"]
			if pathext == "" {
				pathext = ".COM;.EXE;.BAT;.CMD"
			}
			extensions = nil
			for _, ext := range strings.Split(pathext, ";") {
				if ext != "" {
					extensions = append(extensions, strings.ToLower(ext), strings.ToUpper(ext))
				}
			}
		}
		for _, ext := range extensions {
			candidate := filepath.Join(directory, "rg"+ext)
			key := candidate
			if runtime.GOOS == "windows" {
				key = strings.ToLower(key)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			if !usable(candidate) {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			command := exec.CommandContext(ctx, candidate, "--version")
			command.Dir = n.CWD
			command.Env = nativeAgentProcessEnvironment(n)
			output, err := command.Output()
			cancel()
			if err == nil && strings.HasPrefix(string(output), "ripgrep ") {
				return nativeAgentRipgrep{candidate, []string{}}, nil
			}
		}
	}
	return nativeAgentRipgrep{}, &exec.Error{Name: "rg", Err: exec.ErrNotFound}
}
