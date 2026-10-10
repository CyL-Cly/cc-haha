package server

import (
	"context"
	"math"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type recentProject struct {
	ProjectPath  string  `json:"projectPath"`
	RealPath     string  `json:"realPath"`
	ProjectName  string  `json:"projectName"`
	IsGit        bool    `json:"isGit"`
	RepoName     *string `json:"repoName"`
	Branch       *string `json:"branch"`
	ModifiedAt   string  `json:"modifiedAt"`
	SessionCount int     `json:"sessionCount"`
}
type recentProjectCache struct {
	projects []recentProject
	scan     int
	updated  time.Time
}

var parseIntPrefix = regexp.MustCompile(`^[+-]?[0-9]+`)

func sourceParseInt(value string) (int, bool) {
	prefix := parseIntPrefix.FindString(strings.TrimSpace(value))
	if prefix == "" {
		return 0, false
	}
	value64, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		if strings.HasPrefix(prefix, "-") {
			return -5001, true
		}
		return 5001, true
	}
	if value64 > 5001 {
		return 5001, true
	}
	if value64 < -5001 {
		return -5001, true
	}
	return int(value64), true
}
func clamp(value, minimum, maximum int) int { return min(maximum, max(minimum, value)) }

func (server *Server) gitInfoCommand(ctx context.Context, workDir string, arguments ...string) *string {
	env := map[string]string{"HOME": server.options.HomeDir, "USERPROFILE": server.options.HomeDir, "CLAUDE_CONFIG_DIR": server.options.ConfigDir, "PATH": os.Getenv("PATH")}
	if server.options.Environment != nil {
		for key, value := range server.options.Environment() {
			env[key] = value
		}
	}
	timeout := 3 * time.Second
	if millis, err := strconv.ParseFloat(strings.TrimSpace(env["CC_HAHA_GIT_INFO_TIMEOUT_MS"]), 64); err == nil && millis > 0 && !math.IsInf(millis, 0) {
		if millis > 2147483647 || millis < 1 {
			millis = 1
		}
		timeout = time.Duration(math.Trunc(millis)) * time.Millisecond
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	executable, err := executableUsingPath("git", env)
	if err != nil {
		return nil
	}
	command := exec.CommandContext(commandContext, executable, arguments...)
	command.Dir = workDir
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		command.Env = append(command.Env, key+"="+env[key])
	}
	output, err := command.Output()
	if err != nil {
		return nil
	}
	text := strings.TrimSpace(string(output))
	return &text
}

var remoteOwnerPatterns = []*regexp.Regexp{regexp.MustCompile(`:([^/]+/[^/]+?)(?:\.git)?$`), regexp.MustCompile(`/([^/]+/[^/]+?)(?:\.git)?$`)}

func remoteOwnerName(remote *string) *string {
	if remote == nil || *remote == "" {
		return nil
	}
	for _, pattern := range remoteOwnerPatterns {
		if match := pattern.FindStringSubmatch(*remote); match != nil {
			return &match[1]
		}
	}
	return nil
}
func recentName(path, fallback string) string {
	normalized := strings.ReplaceAll(path, "\\", "/")
	if at := strings.Index(normalized, "/.claude/worktrees/"); at >= 0 {
		normalized = normalized[:at]
	}
	parts := strings.FieldsFunc(normalized, func(r rune) bool { return r == '/' })
	if len(parts) == 0 {
		return fallback
	}
	return parts[len(parts)-1]
}

func (server *Server) recentProjectsHTTP(writer http.ResponseWriter, request *http.Request) error {
	if request.Method != http.MethodGet {
		return methodNotAllowed(request.Method)
	}
	limit, ok := sourceParseInt(request.URL.Query().Get("limit"))
	if !ok || limit == 0 {
		limit = 10
	}
	limit = clamp(limit, 1, 500)
	scan, ok := sourceParseInt(request.URL.Query().Get("scan"))
	if ok {
		scan = clamp(scan, 100, 5000)
	} else {
		scan = clamp(limit*16, 100, 500)
	}
	server.mu.Lock()
	cache := server.recentProjects
	server.mu.Unlock()
	if cache != nil && cache.scan >= scan && time.Since(cache.updated) < 30*time.Second {
		projects := cache.projects
		if len(projects) > limit {
			projects = projects[:limit]
		}
		writeJSON(writer, 200, map[string]any{"projects": projects})
		return nil
	}
	all, err := server.allSessions(request.Context())
	if err != nil {
		return err
	}
	if len(all) > scan {
		all = all[:scan]
	}
	projects := []recentProject{}
	positions := map[string]int{}
	for _, session := range all {
		if session.WorkspaceState == "missing" {
			continue
		}
		path := session.WorkDir
		if session.ProjectRoot != nil && *session.ProjectRoot != "" {
			path = *session.ProjectRoot
		}
		if path == "" {
			continue
		}
		if position, exists := positions[path]; exists {
			projects[position].SessionCount++
			if session.ModifiedAt > projects[position].ModifiedAt {
				projects[position].ModifiedAt = session.ModifiedAt
			}
			continue
		}
		positions[path] = len(projects)
		projects = append(projects, recentProject{ProjectPath: path, RealPath: path, ProjectName: recentName(path, session.ProjectPath), ModifiedAt: session.ModifiedAt, SessionCount: 1})
	}
	var group sync.WaitGroup
	for index := range projects {
		project := &projects[index]
		if server.findCachedGitRoot(project.RealPath) == "" {
			continue
		}
		project.IsGit = true
		group.Add(2)
		go func() {
			defer group.Done()
			project.Branch = server.gitInfoCommand(request.Context(), project.RealPath, "rev-parse", "--abbrev-ref", "HEAD")
			if project.Branch != nil && strings.HasPrefix(*project.Branch, "worktree-desktop-") {
				project.Branch = nil
			}
		}()
		go func() {
			defer group.Done()
			project.RepoName = remoteOwnerName(server.gitInfoCommand(request.Context(), project.RealPath, "remote", "get-url", "origin"))
		}()
	}
	group.Wait()
	sort.SliceStable(projects, func(a, b int) bool { return projects[a].ModifiedAt > projects[b].ModifiedAt })
	server.mu.Lock()
	server.recentProjects = &recentProjectCache{projects: projects, scan: scan, updated: time.Now()}
	server.mu.Unlock()
	if len(projects) > limit {
		projects = projects[:limit]
	}
	writeJSON(writer, 200, map[string]any{"projects": projects})
	return nil
}

func (server *Server) invalidateRecentProjects() {
	server.mu.Lock()
	server.recentProjects = nil
	server.mu.Unlock()
}
