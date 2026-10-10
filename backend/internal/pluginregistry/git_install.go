package pluginregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	whatwg "github.com/nlnwa/whatwg-url/url"
)

var githubShorthand = regexp.MustCompile(`^[a-zA-Z0-9-_.]+/[a-zA-Z0-9-_.]+$`)
var sshGitURL = regexp.MustCompile(`^git@[a-zA-Z0-9.-]+:`)

func ValidateGitURL(value string) (string, error) {
	parsed, err := whatwg.NewParser().Parse(value)
	if err != nil {
		if sshGitURL.MatchString(value) {
			return value, nil
		}
		return "", fmt.Errorf("Invalid git URL: %s", value)
	}
	if parsed.Scheme() != "https" && parsed.Scheme() != "http" && parsed.Scheme() != "file" && !sshGitURL.MatchString(value) {
		return "", fmt.Errorf("Invalid git URL: %s", value)
	}
	return value, nil
}
func (m *Materializer) githubURL(value string) string {
	if envTruthy(m.options.Environment["CLAUDE_CODE_REMOTE"]) {
		return "https://github.com/" + value + ".git"
	}
	return "git@github.com:" + value + ".git"
}
func (m *Materializer) GitClone(ctx context.Context, url, target, ref, sha string) error {
	args := []string{"clone", "--depth", "1", "--recurse-submodules", "--shallow-submodules"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	if sha != "" {
		args = append(args, "--no-checkout")
	}
	args = append(args, url, m.ioPath(target))
	result := m.run(ctx, m.options.GitExecutable, args, m.options.CWD)
	if result.Code != 0 {
		return fmt.Errorf("Failed to clone repository: %s", result.Stderr)
	}
	if sha != "" {
		if result := m.run(ctx, m.options.GitExecutable, []string{"fetch", "--depth", "1", "origin", sha}, target); result.Code != 0 {
			result := m.run(ctx, m.options.GitExecutable, []string{"fetch", "--unshallow"}, target)
			if result.Code != 0 {
				return fmt.Errorf("Failed to fetch commit %s: %s", sha, result.Stderr)
			}
		}
		result := m.run(ctx, m.options.GitExecutable, []string{"checkout", sha}, target)
		if result.Code != 0 {
			return fmt.Errorf("Failed to checkout commit %s: %s", sha, result.Stderr)
		}
	}
	return nil
}
func (m *Materializer) GitSubdir(ctx context.Context, url, target, subdir, ref, sha string) (string, error) {
	if result := m.run(ctx, m.options.GitExecutable, []string{"--version"}, m.options.CWD); result.Code != 0 {
		return "", fmt.Errorf("git-subdir plugin source requires git to be installed and on PATH. Install git (version 2.25 or later for sparse-checkout cone mode) and try again.")
	}
	if githubShorthand.MatchString(url) {
		url = m.githubURL(url)
	} else {
		var err error
		url, err = ValidateGitURL(url)
		if err != nil {
			return "", err
		}
	}
	clone := target + ".clone"
	args := []string{"clone", "--depth", "1", "--filter=tree:0", "--no-checkout"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, url, m.ioPath(clone))
	result := m.run(ctx, m.options.GitExecutable, args, m.options.CWD)
	if result.Code != 0 {
		return "", fmt.Errorf("Failed to clone repository for git-subdir source: %s", result.Stderr)
	}
	defer os.RemoveAll(m.ioPath(clone))
	result = m.run(ctx, m.options.GitExecutable, []string{"sparse-checkout", "set", "--cone", "--", subdir}, clone)
	if result.Code != 0 {
		return "", fmt.Errorf("git sparse-checkout set failed (git >= 2.25 required for cone mode): %s", result.Stderr)
	}
	resolvedSHA := sha
	if sha != "" {
		if result := m.run(ctx, m.options.GitExecutable, []string{"fetch", "--depth", "1", "origin", sha}, clone); result.Code != 0 {
			result := m.run(ctx, m.options.GitExecutable, []string{"fetch", "--unshallow"}, clone)
			if result.Code != 0 {
				return "", fmt.Errorf("Failed to fetch commit %s: %s", sha, result.Stderr)
			}
		}
		result := m.run(ctx, m.options.GitExecutable, []string{"checkout", sha}, clone)
		if result.Code != 0 {
			return "", fmt.Errorf("Failed to checkout commit %s: %s", sha, result.Stderr)
		}
	} else {
		ch := make(chan commandResult, 1)
		go func() { ch <- m.run(ctx, m.options.GitExecutable, []string{"rev-parse", "HEAD"}, clone) }()
		checkout := m.run(ctx, m.options.GitExecutable, []string{"checkout", "HEAD"}, clone)
		head := <-ch
		if checkout.Code != 0 {
			return "", fmt.Errorf("git checkout after sparse-checkout failed: %s", checkout.Stderr)
		}
		if head.Code == 0 {
			resolvedSHA = strings.TrimSpace(head.Stdout)
		}
	}
	path, err := PathWithinBase(m.ioPath(clone), subdir)
	if err != nil {
		return "", err
	}
	if err := os.Rename(path, m.ioPath(target)); err != nil {
		if os.IsNotExist(err) {
			suffix := ""
			if ref != "" {
				suffix = " (ref: " + ref + ")"
			}
			return "", fmt.Errorf("Subdirectory '%s' not found in repository %s%s. Check that the path is correct and exists at the specified ref/sha.", subdir, url, suffix)
		}
		return "", err
	}
	return resolvedSHA, nil
}
func (m *Materializer) install(ctx context.Context, source json.RawMessage, target string) (string, error) {
	if jsonKind(source) == "string" {
		var path string
		_ = json.Unmarshal(source, &path)
		if !m.exists(path) {
			return "", fmt.Errorf("Source path does not exist: %s", path)
		}
		if err := CopyDirectory(ctx, m.ioPath(path), m.ioPath(target)); err != nil {
			return "", err
		}
		return "", os.RemoveAll(filepath.Join(m.ioPath(target), ".git"))
	}
	s := object(source)
	switch s.String("source") {
	case "npm":
		cache := filepath.Join(m.options.Directories.Primary, "npm-cache")
		if err := os.MkdirAll(m.ioPath(cache), 0777); err != nil {
			return "", err
		}
		packagePath := filepath.Join(cache, "node_modules", s.String("package"))
		if !m.exists(packagePath) {
			spec := s.String("package")
			if s.String("version") != "" {
				spec += "@" + s.String("version")
			}
			args := []string{"install", spec, "--prefix", m.ioPath(cache)}
			if s.String("registry") != "" {
				args = append(args, "--registry", s.String("registry"))
			}
			result := m.run(ctx, m.options.NpmExecutable, args, m.options.CWD)
			if result.Code != 0 {
				return "", fmt.Errorf("Failed to install npm package: %s", result.Stderr)
			}
		}
		return "", CopyDirectory(ctx, m.ioPath(packagePath), m.ioPath(target))
	case "github":
		if !githubShorthand.MatchString(s.String("repo")) {
			return "", fmt.Errorf("Invalid GitHub repository format: %s. Expected format: owner/repo", s.String("repo"))
		}
		return "", m.GitClone(ctx, m.githubURL(s.String("repo")), target, s.String("ref"), s.String("sha"))
	case "url":
		url, err := ValidateGitURL(s.String("url"))
		if err != nil {
			return "", err
		}
		return "", m.GitClone(ctx, url, target, s.String("ref"), s.String("sha"))
	case "git-subdir":
		return m.GitSubdir(ctx, s.String("url"), target, s.String("path"), s.String("ref"), s.String("sha"))
	case "pip":
		return "", fmt.Errorf("Python package plugins are not yet supported")
	default:
		return "", fmt.Errorf("Unsupported plugin source type")
	}
}
