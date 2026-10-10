package settings

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type GitIgnoreOptions struct {
	HomeDir     string
	Environment func() map[string]string
	Executable  string
}

type GitIgnore struct{ options GitIgnoreOptions }

func NewGitIgnore(options GitIgnoreOptions) (*GitIgnore, error) {
	if options.HomeDir == "" || options.Environment == nil || options.Executable == "" {
		return nil, errors.New("gitignore requires explicit home/environment and git executable")
	}
	return &GitIgnore{options: options}, nil
}

// AddFileGlobRule follows gitignore.ts: detect a .git file/directory up the
// tree, ask git to resolve all ignore sources, and append to HOME/.config/git/
// ignore only when needed. Command failures count as "not ignored", matching
// execFileNoThrow. The caller logs any filesystem error as best-effort work.
func (gitIgnore *GitIgnore) AddFileGlobRule(ctx context.Context, filename, cwd string) error {
	if !directoryInGitRepo(cwd) {
		return nil
	}
	entry := "**/" + filename
	path := filename
	if strings.HasSuffix(filename, "/") {
		path += "sample-file.txt"
	}
	if gitIgnore.ignored(ctx, path, cwd) {
		return nil
	}
	global := filepath.Join(gitIgnore.options.HomeDir, ".config", "git", "ignore")
	if err := os.MkdirAll(filepath.Dir(global), 0777); err != nil {
		return err
	}
	content, err := os.ReadFile(global)
	if err == nil {
		if strings.Contains(string(content), entry) {
			return nil
		}
		output, err := os.OpenFile(global, os.O_APPEND|os.O_WRONLY, 0666)
		if err != nil {
			return err
		}
		_, err = output.WriteString("\n" + entry + "\n")
		closeError := output.Close()
		if err != nil {
			return err
		}
		return closeError
	}
	if errors.Is(err, fs.ErrNotExist) {
		return os.WriteFile(global, []byte(entry+"\n"), 0666)
	}
	return err
}

func (gitIgnore *GitIgnore) ignored(ctx context.Context, path, cwd string) bool {
	environment := gitIgnore.options.Environment()
	executable := gitIgnore.options.Executable
	if !filepath.IsAbs(executable) {
		executable = findExecutable(executable, environment)
		if executable == "" {
			return false
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	command := gitCommand(ctx, executable, []string{"check-ignore", path}, environment)
	if command == nil {
		return false
	}
	command.Dir = cwd
	command.Env = []string{}
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	return command.Run() == nil
}

func findExecutable(name string, environment map[string]string) string {
	extensions := []string{""}
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		extensions = strings.Split(environmentValue(environment, "PATHEXT"), ";")
		if len(extensions) == 1 && extensions[0] == "" {
			extensions = []string{".exe", ".com", ".bat", ".cmd"}
		}
	}
	for _, directory := range filepath.SplitList(environmentValue(environment, "PATH")) {
		if directory == "" || !filepath.IsAbs(directory) {
			continue
		}
		for _, extension := range extensions {
			path := filepath.Join(directory, name+extension)
			if info, err := os.Stat(path); err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode().Perm()&0111 != 0) {
				return path
			}
		}
	}
	return ""
}

func environmentValue(environment map[string]string, key string) string {
	if runtime.GOOS != "windows" {
		return environment[key]
	}
	for name, value := range environment {
		if strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}

func directoryInGitRepo(start string) bool {
	current, err := filepath.Abs(start)
	if err != nil {
		return false
	}
	for {
		if info, err := os.Stat(filepath.Join(current, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}
