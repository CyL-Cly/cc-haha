package ccrsessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

type Repository struct{ Host, Owner, Name string }
type RepositoryValidation struct {
	Status                                             string
	SessionRepo, CurrentRepo, SessionHost, CurrentHost string
}

var sshRemote = regexp.MustCompile(`^git@([^:]+):([^/]+)/([^/]+?)(?:\.git)?$`)
var urlRemote = regexp.MustCompile(`^(https?|ssh|git)://(?:[^@]+@)?([^/:]+(?::\d+)?)/([^/]+)/([^/]+?)(?:\.git)?$`)
var alphaTLD = regexp.MustCompile(`^[a-zA-Z]+$`)
var portSuffix = regexp.MustCompile(`:\d+$`)

func (s *Service) UseDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("CCR checkout requires an absolute native directory")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("CCR checkout is not a directory")
	}
	s.options.CWD = path
	return nil
}

func ParseRepository(value string) *Repository {
	value = strings.TrimSpace(value)
	valid := func(host string) bool {
		parts := strings.Split(host, ".")
		return len(parts) > 1 && alphaTLD.MatchString(parts[len(parts)-1])
	}
	if match := sshRemote.FindStringSubmatch(value); match != nil && valid(match[1]) {
		return &Repository{match[1], match[2], match[3]}
	}
	if match := urlRemote.FindStringSubmatch(value); match != nil && valid(strings.Split(match[2], ":")[0]) {
		host := match[2]
		if match[1] != "http" && match[1] != "https" {
			host = strings.Split(host, ":")[0]
		}
		return &Repository{host, match[3], match[4]}
	}
	return nil
}

func (s *Service) git(ctx context.Context, args ...string) (worktree.ProcessResult, error) {
	ctx, done, err := s.operation(ctx)
	if err != nil {
		return worktree.ProcessResult{}, err
	}
	defer done()
	return s.runGit(ctx, args...)
}
func (s *Service) runGit(ctx context.Context, args ...string) (worktree.ProcessResult, error) {
	env := cloneEnvironment(s.options.Environment)
	env["GIT_TERMINAL_PROMPT"] = "0"
	return s.options.Run(ctx, worktree.ProcessRequest{Command: worktree.NativeGitExecutable(env), Arguments: args, CWD: s.options.CWD, Environment: env, IgnoreStdin: true})
}
func (s *Service) Repository(ctx context.Context) *Repository {
	// Source getRemoteUrl prefers origin, then a first configured remote.
	result, err := s.git(ctx, "config", "--get", "remote.origin.url")
	if err == nil && result.Code == 0 {
		return ParseRepository(result.Stdout)
	}
	remotes, err := s.git(ctx, "remote")
	if err != nil || remotes.Code != 0 {
		return nil
	}
	for _, remote := range strings.Fields(remotes.Stdout) {
		result, err := s.git(ctx, "config", "--get", "remote."+remote+".url")
		if err == nil && result.Code == 0 {
			return ParseRepository(result.Stdout)
		}
	}
	return nil
}

func (s *Service) ValidateRepository(ctx context.Context, session protocol.JSONObject) RepositoryValidation {
	current := s.Repository(ctx)
	context, _ := session.Object("session_context")
	var sources []protocol.JSONObject
	_ = jsonRows(context.Get("sources"), &sources)
	var target *Repository
	for _, source := range sources {
		if source.String("type") == "git_repository" {
			target = ParseRepository(source.String("url"))
			if target == nil {
				value := source.String("url")
				parts := strings.Split(value, "/")
				if len(parts) == 2 && parts[0] != "" && parts[1] != "" && !strings.ContainsAny(value, "@:") {
					target = &Repository{"", parts[0], strings.TrimSuffix(parts[1], ".git")}
				}
			}
			break
		}
	}
	if target == nil {
		return RepositoryValidation{Status: "no_repo_required"}
	}
	result := RepositoryValidation{Status: "not_in_repo", SessionRepo: target.Owner + "/" + target.Name, SessionHost: target.Host}
	if current == nil {
		return result
	}
	result.CurrentRepo, result.CurrentHost = current.Owner+"/"+current.Name, current.Host
	result.Status = "mismatch"
	if strings.EqualFold(result.CurrentRepo, result.SessionRepo) && (target.Host == "" || strings.EqualFold(portSuffix.ReplaceAllString(target.Host, ""), portSuffix.ReplaceAllString(current.Host, ""))) {
		result.Status = "match"
	}
	return result
}
func (v RepositoryValidation) Error(sid string) error {
	if v.Status == "match" || v.Status == "no_repo_required" {
		return nil
	}
	target, current := v.SessionRepo, v.CurrentRepo
	if v.Status == "not_in_repo" && v.SessionHost != "" && !strings.EqualFold(v.SessionHost, "github.com") {
		target = v.SessionHost + "/" + target
	}
	if v.Status == "mismatch" && v.SessionHost != "" && v.CurrentHost != "" && !strings.EqualFold(portSuffix.ReplaceAllString(v.SessionHost, ""), portSuffix.ReplaceAllString(v.CurrentHost, "")) {
		target, current = v.SessionHost+"/"+target, v.CurrentHost+"/"+current
	}
	message := fmt.Sprintf("You must run claude --teleport %s from a checkout of %s.", sid, target)
	if v.Status == "mismatch" {
		message += "\nThis repo is " + current + "."
	}
	return errors.New(message)
}

func (s *Service) ValidateGitState(ctx context.Context) error {
	result, err := s.git(ctx, "status", "--porcelain", "--untracked-files=no")
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if err != nil {
		return err
	}
	if result.Code == 0 && strings.TrimSpace(result.Stdout) != "" {
		return errors.New("Git working directory is not clean. Please commit or stash your changes before using --teleport.")
	}
	return nil
}
func Branch(session protocol.JSONObject) string {
	context, _ := session.Object("session_context")
	var outcomes []protocol.JSONObject
	_ = jsonRows(context.Get("outcomes"), &outcomes)
	for _, outcome := range outcomes {
		if outcome.String("type") == "git_repository" {
			info, _ := outcome.Object("git_info")
			var branches []string
			_ = jsonRows(info.Get("branches"), &branches)
			if len(branches) > 0 {
				return branches[0]
			}
			return ""
		}
	}
	return ""
}
func (s *Service) CheckoutBranch(ctx context.Context, branch string) error {
	if branch == "" {
		return nil
	}
	fetched, err := s.git(ctx, "fetch", "origin", branch+":"+branch)
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	if err == nil && fetched.Code != 0 && strings.Contains(fetched.Stderr, "refspec") {
		_, _ = s.git(ctx, "fetch", "origin", branch)
	}
	var checkout worktree.ProcessResult
	for _, args := range [][]string{{"checkout", branch}, {"checkout", "-b", branch, "--track", "origin/" + branch}, {"checkout", "--track", "origin/" + branch}} {
		checkout, err = s.git(ctx, args...)
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if err == nil && checkout.Code == 0 {
			upstream, _ := s.git(ctx, "rev-parse", "--abbrev-ref", branch+"@{upstream}")
			if upstream.Code != 0 {
				remote, _ := s.git(ctx, "rev-parse", "--verify", "origin/"+branch)
				if remote.Code == 0 {
					_, _ = s.git(ctx, "branch", "--set-upstream-to", "origin/"+branch, branch)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("Failed to checkout branch '%s': %s", branch, checkout.Stderr)
}
