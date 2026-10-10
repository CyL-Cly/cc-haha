package sandboxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LinuxFilesystem owns only the mount points the original wrapper records.
// It does not remove a nonempty file/directory written after the command.
type LinuxFilesystem struct {
	Paths              PathContext
	TemporaryDirectory string
	RipgrepCommand     string
	RipgrepArguments   []string
	Environment        []string
	SearchDepth        int
	AllowGitConfig     bool
	mu                 sync.Mutex
	mountPoints        []string
	tracking           *LinuxFilesystem
}

func exists(path string) bool       { _, e := os.Stat(path); return e == nil }
func within(path, base string) bool { return path == base || strings.HasPrefix(path, base+"/") }
func findLinuxSymlink(path string, allowed []string) string {
	current := ""
	for _, part := range strings.Split(path, "/") {
		if part == "" {
			continue
		}
		current += "/" + part
		stat, e := os.Lstat(current)
		if e != nil {
			break
		}
		if stat.Mode()&os.ModeSymlink != 0 {
			for _, base := range allowed {
				if within(current, base) {
					return current
				}
			}
		}
	}
	return ""
}
func linuxHasFileAncestor(path string) bool {
	current := ""
	for _, part := range strings.Split(path, "/") {
		if part == "" {
			continue
		}
		current += "/" + part
		stat, e := os.Stat(current)
		if e != nil {
			break
		}
		if stat.Mode().IsRegular() {
			return true
		}
	}
	return false
}
func firstMissingComponent(path string) string {
	current := ""
	for _, part := range strings.Split(path, "/") {
		if part == "" {
			continue
		}
		current += "/" + part
		if !exists(current) {
			return current
		}
	}
	return path
}
func (f *LinuxFilesystem) snapshot(cwd string, ripgrep protocol.JSONObject, environment map[string]string) *LinuxFilesystem {
	args := []string{}
	_ = json.Unmarshal(ripgrep.Get("args"), &args)
	paths := f.Paths
	paths.CWD = cwd
	var env []string
	if environment != nil {
		env = []string{}
		for key, value := range environment {
			env = append(env, key+"="+value)
		}
	}
	return &LinuxFilesystem{Environment: env, Paths: paths, TemporaryDirectory: f.TemporaryDirectory, RipgrepCommand: ripgrep.String("command"), RipgrepArguments: args, SearchDepth: f.SearchDepth, AllowGitConfig: f.AllowGitConfig, tracking: f}
}
func (f *LinuxFilesystem) track(path string) {
	if f.tracking != nil {
		f.tracking.track(path)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, old := range f.mountPoints {
		if old == path {
			return
		}
	}
	f.mountPoints = append(f.mountPoints, path)
}
func (f *LinuxFilesystem) Cleanup() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, path := range f.mountPoints {
		stat, e := os.Stat(path)
		if e != nil {
			continue
		}
		if stat.Mode().IsRegular() && stat.Size() == 0 {
			_ = os.Remove(path)
		} else if stat.IsDir() {
			entries, e := os.ReadDir(path)
			if e == nil && len(entries) == 0 {
				_ = os.Remove(path)
			}
		}
	}
	f.mountPoints = nil
}
func (f *LinuxFilesystem) mandatory(ctx context.Context) []string {
	cwd := resolvedWorkingDirectory(f.Paths)
	paths := []string{}
	for _, name := range dangerousFiles {
		paths = append(paths, filepath.Join(cwd, name))
	}
	for _, name := range dangerousDirectories {
		paths = append(paths, filepath.Join(cwd, name))
	}
	if stat, e := os.Stat(filepath.Join(cwd, ".git")); e == nil && stat.IsDir() {
		paths = append(paths, filepath.Join(cwd, ".git/hooks"))
		if !f.AllowGitConfig {
			paths = append(paths, filepath.Join(cwd, ".git/config"))
		}
	}
	globs := []string{}
	for _, file := range dangerousFiles {
		globs = append(globs, "--iglob", file)
	}
	for _, dir := range dangerousDirectories {
		globs = append(globs, "--iglob", "**/"+dir+"/**")
	}
	globs = append(globs, "--iglob", "**/.git/hooks/**")
	if !f.AllowGitConfig {
		globs = append(globs, "--iglob", "**/.git/config")
	}
	args := append(cloneSlice(f.RipgrepArguments), "--files", "--hidden", "--max-depth", strconv.Itoa(f.SearchDepth))
	args = append(args, globs...)
	args = append(args, "-g", "!**/node_modules/**")
	args = append(args, cwd)
	active, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(active, f.RipgrepCommand, args...)
	cmd.Dir = cwd
	if f.Environment != nil {
		cmd.Env = cloneSlice(f.Environment)
	}
	// Only the existing fixed CLI primitive is invoked; no provider/helper or
	// business code is evaluated. Original ripGrep failures leave mandatory roots.
	output, e := cmd.Output()
	if e == nil {
		for _, match := range strings.Split(protocol.TrimJavaScriptString(string(output)), "\n") {
			if match == "" {
				continue
			}
			absolute := match
			if !filepath.IsAbs(absolute) {
				absolute = filepath.Join(cwd, match)
			} else {
				absolute = filepath.Clean(absolute)
			}
			found := false
			for _, dir := range append(cloneSlice(dangerousDirectories), ".git") {
				segments := strings.Split(absolute, "/")
				index := -1
				for i, segment := range segments {
					if strings.ToLower(segment) == strings.ToLower(dir) {
						index = i
						break
					}
				}
				if index >= 0 {
					base := strings.Join(segments[:index+1], "/")
					if dir == ".git" {
						if strings.Contains(match, ".git/hooks") {
							paths = append(paths, filepath.Join(base, "hooks"))
						} else if strings.Contains(match, ".git/config") {
							paths = append(paths, filepath.Join(base, "config"))
						}
					} else {
						paths = append(paths, base)
					}
					found = true
					break
				}
			}
			if !found {
				paths = append(paths, absolute)
			}
		}
	}
	result := []string{}
	seen := map[string]bool{}
	for _, path := range paths {
		if !seen[path] {
			result = append(result, path)
			seen[path] = true
		}
	}
	return result
}

// Arguments performs the original native filesystem observations in their
// original order. The generated binds are execution input, never authorization.
func (f *LinuxFilesystem) Arguments(ctx context.Context, read *ReadRestriction, write *WriteRestriction) ([]string, error) {
	if !filepath.IsAbs(f.Paths.CWD) || !filepath.IsAbs(f.Paths.Home) || f.Paths.Realpath == nil || f.RipgrepCommand == "" || !filepath.IsAbs(f.TemporaryDirectory) {
		return nil, errors.New("Linux filesystem requires actual cwd/home/native paths/ripgrep/temp")
	}
	f.mu.Lock()
	if f.mountPoints == nil {
		f.mountPoints = []string{}
	}
	f.mu.Unlock()
	args := []string{}
	allowed := []string{}
	denyArgs := []string{}
	normalize := func(path string) string { return NormalizePath(path, f.Paths) }
	if write != nil {
		args = append(args, "--ro-bind", "/", "/")
		for _, pattern := range write.AllowOnly {
			path := normalize(pattern)
			if strings.HasPrefix(path, "/dev/") || !exists(path) {
				continue
			}
			resolved, e := f.Paths.Realpath(path)
			if e != nil {
				continue
			}
			if resolved != strings.TrimRight(path, "/") && SymlinkOutsideBoundary(path, resolved) {
				continue
			}
			args = append(args, "--bind", path, path)
			allowed = append(allowed, path)
		}
		deny := append(cloneSlice(write.DenyWithinAllow), f.mandatory(ctx)...)
		seen := map[string]bool{}
		for _, pattern := range deny {
			path := normalize(pattern)
			if seen[path] {
				continue
			}
			seen[path] = true
			if strings.HasPrefix(path, "/dev/") {
				continue
			}
			if symlink := findLinuxSymlink(path, allowed); symlink != "" {
				denyArgs = append(denyArgs, "--ro-bind", "/dev/null", symlink)
				continue
			}
			if !exists(path) {
				if linuxHasFileAncestor(path) {
					continue
				}
				ancestor := filepath.Dir(path)
				for ancestor != "/" && !exists(ancestor) {
					ancestor = filepath.Dir(ancestor)
				}
				protected := false
				for _, base := range allowed {
					protected = protected || within(ancestor, base) || strings.HasPrefix(path, base+"/")
				}
				if protected {
					missing := firstMissingComponent(path)
					mount := "/dev/null"
					if missing != path {
						var e error
						mount, e = os.MkdirTemp(f.TemporaryDirectory, "claude-empty-")
						if e != nil {
							return nil, e
						}
					}
					denyArgs = append(denyArgs, "--ro-bind", mount, missing)
					f.track(missing)
				}
				continue
			}
			for _, base := range allowed {
				if within(path, base) {
					denyArgs = append(denyArgs, "--ro-bind", path, path)
					break
				}
			}
		}
	} else {
		args = append(args, "--bind", "/", "/")
	}
	readAllow := []string{}
	readDeny := []string{}
	if read != nil {
		for _, path := range read.AllowWithinDeny {
			readAllow = append(readAllow, normalize(path))
		}
		for _, path := range read.DenyOnly {
			if normalize(path) == "/" {
				children, e := os.ReadDir("/")
				if e != nil {
					return nil, e
				}
				for _, child := range children {
					if child.Name() != "proc" && child.Name() != "dev" && child.Name() != "sys" {
						readDeny = append(readDeny, "/"+child.Name())
					}
				}
			} else {
				readDeny = append(readDeny, path)
			}
		}
	}
	if exists("/etc/ssh/ssh_config.d") {
		readDeny = append(readDeny, "/etc/ssh/ssh_config.d")
	}
	for _, pattern := range readDeny {
		path := normalize(pattern)
		if !exists(path) {
			continue
		}
		stat, e := os.Stat(path)
		if e != nil {
			return nil, e
		}
		sep := path + "/"
		if path == "/" {
			sep = "/"
		}
		if stat.IsDir() {
			args = append(args, "--tmpfs", path)
			for _, writePath := range allowed {
				if strings.HasPrefix(writePath, sep) || writePath == path {
					args = append(args, "--bind", writePath, writePath)
				}
			}
			for _, allow := range readAllow {
				if !(strings.HasPrefix(allow, sep) || allow == path) || !exists(allow) {
					continue
				}
				covered := false
				for _, writePath := range allowed {
					if (strings.HasPrefix(writePath, sep) || writePath == path) && within(allow, writePath) {
						covered = true
						break
					}
				}
				if !covered {
					args = append(args, "--ro-bind", allow, allow)
				}
			}
		} else {
			covered := false
			for _, allow := range readAllow {
				covered = covered || within(path, allow)
			}
			if !covered {
				args = append(args, "--ro-bind", "/dev/null", path)
			}
		}
	}
	return append(args, denyArgs...), nil
}
