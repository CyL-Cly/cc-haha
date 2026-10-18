package server

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type repositoryBranch struct {
	Name         string `json:"name"`
	Current      bool   `json:"current"`
	Local        bool   `json:"local"`
	Remote       bool   `json:"remote"`
	RemoteRef    string `json:"remoteRef,omitempty"`
	CheckedOut   bool   `json:"checkedOut"`
	WorktreePath string `json:"worktreePath,omitempty"`
	Commit       string `json:"commit,omitempty"`
}
type repositoryWorktree struct {
	Path    string  `json:"path"`
	Branch  *string `json:"branch"`
	Current bool    `json:"current"`
}
type repositoryContext struct {
	State         string               `json:"state"`
	WorkDir       string               `json:"workDir"`
	RepoRoot      *string              `json:"repoRoot"`
	RepoName      *string              `json:"repoName"`
	CurrentBranch *string              `json:"currentBranch"`
	DefaultBranch *string              `json:"defaultBranch"`
	HeadCommit    *string              `json:"headCommit"`
	Dirty         bool                 `json:"dirty"`
	Branches      []repositoryBranch   `json:"branches"`
	Worktrees     []repositoryWorktree `json:"worktrees"`
	Error         string               `json:"error,omitempty"`
}
type repositoryGitResult struct {
	stdout, stderr string
	code           int
	failedToRun    bool
}

// Node's execFile maxBuffer is independent for stdout/stderr. Terminate the
// native child after crossing either bound and retain only its bounded prefix.
type repositoryOutput struct {
	data     []byte
	cancel   context.CancelFunc
	overflow bool
	maximum  int
}

func (output *repositoryOutput) Write(data []byte) (int, error) {
	maximum := output.maximum
	if maximum == 0 {
		maximum = 2000000
	}
	length := len(data)
	remaining := maximum - len(output.data)
	output.data = append(output.data, data[:min(remaining, len(data))]...)
	if length > remaining {
		output.overflow = true
		output.cancel()
	}
	return length, nil
}
func (server *Server) repositoryGit(ctx context.Context, directory string, args ...string) repositoryGitResult {
	return server.repositoryGitConfigured(ctx, directory, nil, 2000000, args...)
}

// Review shares the process boundary while selecting its inherited 8 MB cap and
// removing Git retargeting variables. Other callers retain their existing defaults.
func (server *Server) repositoryGitConfigured(ctx context.Context, directory string, configure func(map[string]string), maximum int, args ...string) repositoryGitResult {
	environment := map[string]string{}
	if server.options.Environment != nil {
		for name, value := range server.options.Environment() {
			environment[name] = value
		}
	} else {
		environment["PATH"] = os.Getenv("PATH")
	}
	environment["HOME"], environment["USERPROFILE"] = server.options.HomeDir, server.options.HomeDir
	environment["CLAUDE_CONFIG_DIR"] = server.options.ConfigDir
	environment["GIT_TERMINAL_PROMPT"], environment["GIT_ASKPASS"] = "0", ""
	if configure != nil {
		configure(environment)
	}
	executable, err := executableUsingPath("git", environment)
	if err != nil {
		return repositoryGitResult{stderr: err.Error(), code: 1, failedToRun: true}
	}
	commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(commandContext, executable, args...)
	command.Dir = directory
	for name, value := range environment {
		command.Env = append(command.Env, name+"="+value)
	}
	stdout, stderr := &repositoryOutput{cancel: cancel, maximum: maximum}, &repositoryOutput{cancel: cancel, maximum: maximum}
	command.Stdout, command.Stderr = stdout, stderr
	err = command.Run()
	result := repositoryGitResult{stdout: string(stdout.data), stderr: string(stderr.data)}
	if err != nil {
		result.code = 1
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() >= 0 {
			result.code = exit.ExitCode()
		}
		if result.stderr == "" {
			result.stderr = err.Error()
		}
	}
	if err != nil {
		_, exited := err.(*exec.ExitError)
		result.failedToRun = !exited || commandContext.Err() != nil
	}
	if stdout.overflow || stderr.overflow {
		result.failedToRun = true
		result.code = 1
	}
	return result
}
func parseRepositoryWorktrees(output string) []repositoryWorktree {
	records := []repositoryWorktree{}
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			records = append(records, repositoryWorktree{Path: canonicalPath(strings.TrimPrefix(line, "worktree "))})
			continue
		}
		if len(records) > 0 && strings.HasPrefix(line, "branch ") {
			branch := strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
			records[len(records)-1].Branch = &branch
		}
	}
	return records
}
func (server *Server) repositoryBranches(ctx context.Context, root string, current *string, worktrees []repositoryWorktree) []repositoryBranch {
	checkedOut := map[string]string{}
	for _, worktree := range worktrees {
		if worktree.Branch != nil && *worktree.Branch != "" {
			checkedOut[*worktree.Branch] = worktree.Path
		}
	}
	branches := map[string]repositoryBranch{}
	order := []string{}
	add := func(branch repositoryBranch) {
		if _, exists := branches[branch.Name]; !exists {
			order = append(order, branch.Name)
		}
		branches[branch.Name] = branch
	}
	for _, remote := range []bool{false, true} {
		kind := "refs/heads"
		if remote {
			kind = "refs/remotes"
		}
		result := server.repositoryGit(ctx, root, "for-each-ref", "--format=%(refname:short)%09%(objectname)", kind)
		if result.code != 0 {
			continue
		}
		for _, line := range strings.Split(result.stdout, "\n") {
			line = trimJS(line)
			if line == "" {
				continue
			}
			ref, commit, _ := strings.Cut(line, "\t")
			if ref == "" {
				continue
			}
			if !remote {
				worktreePath := checkedOut[ref]
				add(repositoryBranch{Name: ref, Current: current != nil && ref == *current, Local: true, CheckedOut: worktreePath != "", WorktreePath: worktreePath, Commit: commit})
				continue
			}
			if strings.HasSuffix(ref, "/HEAD") {
				continue
			}
			origin, name, found := strings.Cut(ref, "/")
			if !found || origin == "" || name == "" {
				continue
			}
			if origin != "origin" {
				name = ref
			}
			if branch, exists := branches[name]; exists {
				branch.Remote = true
				branch.RemoteRef = ref
				add(branch)
			} else {
				add(repositoryBranch{Name: name, Current: current != nil && name == *current, Remote: true, RemoteRef: ref, Commit: commit})
			}
		}
	}
	if current != nil && *current != "" {
		if _, exists := branches[*current]; !exists {
			worktreePath := checkedOut[*current]
			add(repositoryBranch{Name: *current, Current: true, Local: true, CheckedOut: worktreePath != "", WorktreePath: worktreePath})
		}
	}
	result := []repositoryBranch{}
	for _, name := range order {
		if !strings.HasPrefix(name, "worktree-desktop-") {
			result = append(result, branches[name])
		}
	}
	collator := collate.New(language.English)
	sort.SliceStable(result, func(a, b int) bool {
		left, right := result[a], result[b]
		if left.Current != right.Current {
			return left.Current
		}
		if left.Local != right.Local {
			return left.Local
		}
		return collator.CompareString(left.Name, right.Name) < 0
	})
	return result
}
func (server *Server) defaultRepositoryBranch(ctx context.Context, root string) *string {
	result := server.repositoryGit(ctx, root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if result.code == 0 {
		value := trimJS(result.stdout)
		if strings.HasPrefix(value, "origin/") {
			value = strings.TrimPrefix(value, "origin/")
			return &value
		}
		if value != "" {
			return &value
		}
	}
	result = server.repositoryGit(ctx, root, "branch", "--show-current")
	return nonemptyStringPointer(trimJS(result.stdout))
}
func (server *Server) inspectRepository(ctx context.Context, workDir string) repositoryContext {
	resolved := server.filesystemPath(workDir)
	result := repositoryContext{WorkDir: resolved, Branches: []repositoryBranch{}, Worktrees: []repositoryWorktree{}}
	real, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		result.State = "missing_workdir"
		result.Error = "Working directory does not exist: " + resolved
		return result
	}
	stat, err := os.Stat(real)
	if err != nil || !stat.IsDir() {
		result.State = "missing_workdir"
		result.WorkDir = resolved
		result.Error = "Working directory is not a directory: " + real
		return result
	}
	result.WorkDir = real
	server.RegisterFilesystemAccessRoot(workDir)
	server.RegisterFilesystemAccessRoot(real)
	gitRoot := server.findCachedGitRoot(real)
	if gitRoot == "" {
		result.State = "not_git_repo"
		return result
	}
	root := server.findCachedCanonicalGitRoot(gitRoot)
	if root == "" {
		root = gitRoot
	}
	server.RegisterFilesystemAccessRoot(root)
	var branch, status, worktree, head repositoryGitResult
	var defaultBranch *string
	var group sync.WaitGroup
	group.Add(5)
	go func() { defer group.Done(); branch = server.repositoryGit(ctx, gitRoot, "branch", "--show-current") }()
	go func() { defer group.Done(); defaultBranch = server.defaultRepositoryBranch(ctx, gitRoot) }()
	go func() {
		defer group.Done()
		status = server.repositoryGit(ctx, gitRoot, "--no-optional-locks", "status", "--porcelain")
	}()
	go func() {
		defer group.Done()
		worktree = server.repositoryGit(ctx, root, "worktree", "list", "--porcelain")
	}()
	go func() { defer group.Done(); head = server.repositoryGit(ctx, gitRoot, "rev-parse", "HEAD") }()
	group.Wait()
	result.State = "ok"
	result.RepoRoot = &root
	name := filepath.Base(root)
	result.RepoName = &name
	result.CurrentBranch = nonemptyStringPointer(trimJS(branch.stdout))
	result.DefaultBranch = defaultBranch
	if head.code == 0 {
		result.HeadCommit = nonemptyStringPointer(trimJS(head.stdout))
	}
	result.Dirty = status.code == 0 && trimJS(status.stdout) != ""
	if worktree.code == 0 {
		result.Worktrees = parseRepositoryWorktrees(worktree.stdout)
	}
	for index := range result.Worktrees {
		result.Worktrees[index].Current = sameOrInsideFilesystemPathSource(result.Worktrees[index].Path, real)
	}
	result.Branches = server.repositoryBranches(ctx, root, result.CurrentBranch, result.Worktrees)
	return result
}
func sameOrInsideFilesystemPathSource(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && (relative == "." || relative != "" && !strings.HasPrefix(relative, "..") && !filepath.IsAbs(relative))
}
func (server *Server) repositoryContextHTTP(writer http.ResponseWriter, request *http.Request) error {
	workDir := request.URL.Query().Get("workDir")
	if workDir == "" {
		return badRequest("workDir query parameter is required")
	}
	result := server.inspectRepository(request.Context(), workDir)
	// The collection route also registers a requested path when it is missing,
	// matching Source's directory-picker registration boundary.
	server.RegisterFilesystemAccessRoot(workDir)
	server.RegisterFilesystemAccessRoot(result.WorkDir)
	if result.RepoRoot != nil {
		server.RegisterFilesystemAccessRoot(*result.RepoRoot)
	}
	writeJSON(writer, http.StatusOK, result)
	return nil
}
