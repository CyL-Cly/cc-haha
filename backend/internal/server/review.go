package server

// Git Review is deliberately separate from workspace's session-tool/turn diff.
// The wire contract is inherited from src/server/services/reviewService.ts.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

const reviewNoHead = "Repository has no commits yet, so there is no HEAD to compare against"
const reviewMissingWorkdir = "The session working directory no longer exists, so there is nothing to compare"
const reviewTurnRejection = "Review source \"turn\" is served by the session turn history, not the Git review service"

type reviewSource struct {
	Kind         string `json:"kind"`
	BaseRef      string `json:"baseRef,omitempty"`
	Commit       string `json:"commit,omitempty"`
	ResolvedBase string `json:"resolvedBase,omitempty"`
}
type reviewFile struct {
	Path           string `json:"path"`
	OldPath        string `json:"oldPath,omitempty"`
	Status         string `json:"status"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	Binary         bool   `json:"binary"`
	Staged         bool   `json:"staged"`
	Unstaged       bool   `json:"unstaged"`
	Conflicted     bool   `json:"conflicted"`
	StatsTruncated bool   `json:"statsTruncated,omitempty"`
}
type reviewTotals struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
	Files     int `json:"files"`
}
type reviewRevision struct {
	State    string       `json:"state"`
	Source   reviewSource `json:"source"`
	Snapshot string       `json:"snapshot"`
	Error    string       `json:"error,omitempty"`
}
type reviewStatus struct {
	reviewRevision
	Files     []reviewFile `json:"files"`
	Untracked []string     `json:"untracked"`
	Totals    reviewTotals `json:"totals"`
}
type reviewDiff struct {
	reviewRevision
	Path      string  `json:"path"`
	OldPath   string  `json:"oldPath,omitempty"`
	Diff      *string `json:"diff,omitempty"`
	Binary    *bool   `json:"binary,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
	Bytes     int64   `json:"bytes,omitempty"`
}
type reviewEntry struct {
	path, oldPath, repoPath, repoOldPath, code, absolute string
	untracked, conflicted, staged, unstaged              bool
}
type reviewContext struct {
	server                              *Server
	request                             context.Context
	info                                sessionInfo
	root, canonicalRoot, repo, fromRepo string
}
type reviewResolved struct {
	source                    reviewSource
	args                      []string
	includeUntracked, working bool
}

func parseReviewSource(raw map[string]json.RawMessage, write bool) (reviewSource, error) {
	if raw == nil {
		return reviewSource{}, badRequest("source is required")
	}
	kind := stringField(raw, "kind")
	source := reviewSource{Kind: kind}
	switch kind {
	case "unstaged", "staged", "uncommitted":
	case "branch":
		source.BaseRef = stringField(raw, "baseRef")
		if source.BaseRef == "" {
			return source, badRequest("baseRef is required for the branch source")
		}
	case "commit":
		source.Commit = stringField(raw, "commit")
		if source.Commit == "" {
			return source, badRequest("commit is required for the commit source")
		}
	case "turn":
		return source, badRequest(reviewTurnRejection)
	default:
		display := kind
		if value := raw["kind"]; len(value) > 0 && strings.TrimSpace(string(value)) != "null" {
			if text, err := protocol.JavaScriptStringJSON(value); err == nil {
				_ = json.Unmarshal(text, &display)
			}
		}
		return source, badRequest("Unknown review source: " + display)
	}
	if write && (kind == "branch" || kind == "commit") {
		return source, badRequest(fmt.Sprintf("Review source %q is a read-only comparison and cannot be written to", kind))
	}
	return source, nil
}
func reviewQuerySource(request *http.Request) (reviewSource, error) {
	q := request.URL.Query()
	raw := map[string]json.RawMessage{}
	for name, value := range map[string]string{"kind": q.Get("source"), "baseRef": q.Get("baseRef"), "commit": q.Get("commit")} {
		raw[name], _ = json.Marshal(value)
	}
	return parseReviewSource(raw, false)
}
func (server *Server) reviewHTTP(writer http.ResponseWriter, request *http.Request, info sessionInfo, parts []string) error {
	if info.WorkDir == "" {
		return notFound("Session not found: " + info.ID)
	}
	resource := ""
	if len(parts) > 4 {
		resource = parts[4]
	}
	if resource == "" || resource == "revision" || resource == "diff" {
		if request.Method != http.MethodGet {
			return methodNotAllowed(request.Method)
		}
		if resource == "diff" && request.URL.Query().Get("path") == "" {
			return badRequest("path query parameter is required for review diff")
		}
		source, err := reviewQuerySource(request)
		if err != nil {
			return err
		}
		ctx, failed := server.prepareReview(request.Context(), info, source)
		if resource == "diff" {
			var result reviewDiff
			if failed != nil {
				result = reviewDiff{reviewRevision: *failed, Path: request.URL.Query().Get("path")}
			} else {
				result, err = ctx.diff(source, request.URL.Query().Get("path"), request.URL.Query().Get("oldPath"))
			}
			if err != nil {
				return err
			}
			writeJSON(writer, 200, result)
			return nil
		}
		if failed != nil {
			if resource == "revision" {
				writeJSON(writer, 200, *failed)
			} else {
				writeJSON(writer, 200, reviewEmptyStatus(*failed))
			}
			return nil
		}
		result := ctx.status(source, resource == "revision")
		if resource == "revision" {
			writeJSON(writer, 200, result.reviewRevision)
		} else {
			writeJSON(writer, 200, result)
		}
		return nil
	}
	switch resource {
	case "stage", "unstage", "stage-hunk", "unstage-hunk", "revert":
	default:
		return notFound("Unknown review resource: " + resource)
	}
	if request.Method != http.MethodPost {
		return methodNotAllowed(request.Method)
	}
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return badRequest("Invalid JSON body")
	}
	var body map[string]json.RawMessage
	if !json.Valid(raw) {
		return badRequest("Invalid JSON body")
	}
	if json.Unmarshal(raw, &body) != nil || body == nil {
		return badRequest("Request body must be an object")
	}
	input := reviewWriteRequest{snapshot: stringField(body, "snapshot"), patch: stringField(body, "patch")}
	if input.snapshot == "" {
		return badRequest("snapshot is required")
	}
	if sourceRaw, exists := body["source"]; exists {
		var object map[string]json.RawMessage
		if json.Unmarshal(sourceRaw, &object) != nil {
			return badRequest("source is required")
		}
		source, err := parseReviewSource(object, true)
		if err != nil {
			return err
		}
		input.source = &source
	}
	if strings.HasSuffix(resource, "-hunk") {
		if trimJS(input.patch) == "" {
			return badRequest("patch is required")
		}
	} else {
		var paths []json.RawMessage
		if json.Unmarshal(body["paths"], &paths) != nil || len(paths) == 0 {
			return badRequest("paths must be a non-empty array")
		}
		for _, raw := range paths {
			var path string
			if json.Unmarshal(raw, &path) != nil || trimJS(path) == "" {
				return badRequest("paths must contain non-empty strings")
			}
			input.paths = append(input.paths, path)
		}
	}
	if refusal := reviewWriteRefusal(resource, input.source); refusal != "" {
		writeJSON(writer, 200, reviewWriteFailure("error", refusal))
		return nil
	}
	source := reviewSource{Kind: "unstaged"}
	if input.source != nil {
		source = *input.source
	}
	ctx, failed := server.prepareReview(request.Context(), info, source)
	if failed != nil {
		writeJSON(writer, 200, reviewWriteFailure(failed.State, failed.Error))
		return nil
	}
	result, err := ctx.write(resource, input)
	if err != nil {
		return err
	}
	writeJSON(writer, 200, result)
	return nil
}
func reviewEmptyStatus(revision reviewRevision) reviewStatus {
	return reviewStatus{reviewRevision: revision, Files: []reviewFile{}, Untracked: []string{}}
}
func (server *Server) prepareReview(request context.Context, info sessionInfo, source reviewSource) (*reviewContext, *reviewRevision) {
	fail := func(state, message string) (*reviewContext, *reviewRevision) {
		return nil, &reviewRevision{State: state, Source: source, Error: message}
	}
	root := server.filesystemPath(info.WorkDir)
	stat, err := os.Stat(root)
	if workspaceMissing(err) || err == nil && !stat.IsDir() {
		return fail("missing_workdir", reviewMissingWorkdir)
	}
	if err != nil {
		return fail("error", workspaceFSError("Failed to stat review path", root, err))
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fail("error", workspaceFSError("Failed to canonicalize workspace root", root, err))
	}
	ctx := &reviewContext{server: server, request: request, info: info, root: root, canonicalRoot: canonical}
	result := ctx.git(root, "rev-parse", "--show-toplevel")
	if result.code != 0 || trimJS(result.stdout) == "" {
		if !result.failedToRun {
			probe := ctx.git(root, "rev-parse", "--is-inside-work-tree")
			if !probe.failedToRun && probe.code != 0 {
				return fail("not_git_repo", "")
			}
		}
		return fail("error", workspaceGitError("Failed to inspect git repository", root, result, "rev-parse", "--show-toplevel"))
	}
	repo, err := filepath.EvalSymlinks(filepath.Clean(trimJS(result.stdout)))
	if err != nil {
		return fail("error", workspaceFSError("Failed to canonicalize git repository root", trimJS(result.stdout), err))
	}
	ctx.repo = repo
	from, err := filepath.Rel(repo, canonical)
	if err != nil {
		return fail("error", err.Error())
	}
	ctx.fromRepo = normalizeWorkspacePath(from)
	return ctx, nil
}
func (ctx *reviewContext) git(root string, args ...string) repositoryGitResult {
	result := ctx.server.repositoryGitConfigured(ctx.request, root, func(env map[string]string) {
		for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_PREFIX", "GIT_CEILING_DIRECTORIES"} {
			delete(env, name)
		}
		env["LC_ALL"], env["LANG"], env["GIT_TERMINAL_PROMPT"] = "C", "C", "0"
	}, 8000000, append([]string{"-c", "core.quotePath=false"}, args...)...)
	result.stdout = string(protocol.DecodeJavaScriptUTF8([]byte(result.stdout)))
	result.stderr = string(protocol.DecodeJavaScriptUTF8([]byte(result.stderr)))
	return result
}
func reviewLiteral(path string) string { return ":(literal)" + path }
func (ctx *reviewContext) head() string {
	result := ctx.git(ctx.repo, "rev-parse", "--verify", "HEAD")
	if result.code != 0 {
		return ""
	}
	return trimJS(result.stdout)
}
func (ctx *reviewContext) rebase(path string) (string, bool) {
	if ctx.fromRepo == "" {
		return normalizeWorkspacePath(path), true
	}
	value, err := filepath.Rel(filepath.FromSlash(ctx.fromRepo), filepath.FromSlash(path))
	if err != nil || value == "." || value == ".." || strings.HasPrefix(value, ".."+string(filepath.Separator)) || filepath.IsAbs(value) {
		return "", false
	}
	return normalizeWorkspacePath(value), true
}
func reviewConflict(code string) bool {
	return strings.Contains(code, "U") || code == "AA" || code == "DD"
}
func (ctx *reviewContext) entries() ([]reviewEntry, string) {
	args := []string{"status", "--porcelain=v1", "-z", "--untracked-files=all"}
	result := ctx.git(ctx.repo, args...)
	if result.code != 0 {
		return nil, workspaceGitError("Failed to read git status", ctx.repo, result, args...)
	}
	entries := []reviewEntry{}
	parts := strings.Split(result.stdout, "\x00")
	for i := 0; i < len(parts); i++ {
		record := parts[i]
		if len(record) < 3 {
			continue
		}
		code, path := record[:2], normalizeWorkspacePath(record[3:])
		old := ""
		if strings.ContainsAny(code, "RC") {
			i++
			if i < len(parts) {
				old = normalizeWorkspacePath(parts[i])
			}
		}
		relative, inside := ctx.rebase(path)
		if !inside {
			continue
		}
		oldRelative := ""
		if old != "" {
			oldRelative, _ = ctx.rebase(old)
		}
		untracked, conflicted := code == "??", reviewConflict(code)
		entries = append(entries, reviewEntry{path: relative, oldPath: oldRelative, repoPath: path, repoOldPath: old, code: code, absolute: filepath.Join(ctx.repo, filepath.FromSlash(path)), untracked: untracked, conflicted: conflicted, staged: !untracked && !conflicted && code[0] != ' ', unstaged: !untracked && !conflicted && code[1] != ' '})
	}
	comparator := collate.New(language.English)
	sort.SliceStable(entries, func(i, j int) bool { return comparator.CompareString(entries[i].path, entries[j].path) < 0 })
	return entries, ""
}
func (ctx *reviewContext) snapshot(head string, entries []reviewEntry) string {
	hash := sha256.New()
	if head == "" {
		head = "unborn"
	}
	fmt.Fprintf(hash, "head:%s ", head)
	args := []string{"diff", "--cached", "--raw", "-z", "--find-renames", "--find-copies", "--"}
	if ctx.fromRepo != "" {
		args = append(args, reviewLiteral(ctx.fromRepo))
	}
	result := ctx.git(ctx.repo, args...)
	if result.code == 0 {
		fmt.Fprintf(hash, "index:%s ", result.stdout)
	} else {
		fmt.Fprint(hash, "index:unavailable ")
	}
	for _, entry := range entries {
		fmt.Fprintf(hash, "%s:%s:%s", entry.code, entry.path, entry.oldPath)
		stat, err := os.Stat(entry.absolute)
		if err == nil {
			fmt.Fprintf(hash, ":%s:%d ", strconv.FormatFloat(sourceMtimeMillis(stat), 'f', -1, 64), stat.Size())
		} else {
			fmt.Fprint(hash, ":absent ")
		}
	}
	return hex.EncodeToString(hash.Sum(nil))[:32]
}
func reviewSHA(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func reviewComparisonSnapshot(snapshot string, source reviewSource) string {
	if source.Kind == "commit" {
		raw, _ := json.Marshal(source)
		return reviewSHA(string(raw))
	}
	if source.Kind == "branch" {
		return reviewSHA(snapshot + ":" + source.ResolvedBase)
	}
	return snapshot
}
func (ctx *reviewContext) resolve(source reviewSource, head string) (reviewResolved, string, string) {
	resolved := reviewResolved{source: source, working: true}
	fail := func(state, message string) (reviewResolved, string, string) { return resolved, state, message }
	switch source.Kind {
	case "unstaged":
		resolved.includeUntracked = true
	case "staged", "uncommitted":
		if head == "" {
			return fail("no_head", reviewNoHead)
		}
		resolved.source.ResolvedBase = head
		resolved.args = []string{head}
		if source.Kind == "staged" {
			resolved.args = append([]string{"--cached"}, resolved.args...)
		} else {
			resolved.includeUntracked = true
		}
	case "branch":
		if head == "" {
			return fail("no_head", reviewNoHead)
		}
		if source.BaseRef == "" || strings.HasPrefix(source.BaseRef, "-") {
			return fail("error", "Invalid base ref: "+source.BaseRef)
		}
		base := ctx.git(ctx.repo, "rev-parse", "--verify", source.BaseRef+"^{commit}")
		if base.code != 0 {
			return fail("error", "Unknown base ref: "+source.BaseRef)
		}
		merge := ctx.git(ctx.repo, "merge-base", head, trimJS(base.stdout))
		if merge.code != 0 || trimJS(merge.stdout) == "" {
			return fail("error", "No merge base between HEAD and "+source.BaseRef)
		}
		resolved.source.ResolvedBase = trimJS(merge.stdout)
		resolved.args = []string{resolved.source.ResolvedBase}
	case "commit":
		if source.Commit == "" || strings.HasPrefix(source.Commit, "-") {
			return fail("error", "Invalid commit: "+source.Commit)
		}
		commit := ctx.git(ctx.repo, "rev-parse", "--verify", source.Commit+"^{commit}")
		if commit.code != 0 {
			return fail("error", "Unknown commit: "+source.Commit)
		}
		sha := trimJS(commit.stdout)
		parents := ctx.git(ctx.repo, "rev-list", "--parents", "-n", "1", sha)
		if parents.code != 0 {
			return fail("error", "Failed to resolve parents of "+source.Commit)
		}
		fields := strings.Fields(parents.stdout)
		base := "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
		if len(fields) > 1 {
			base = fields[1]
		} else {
			empty := ctx.git(ctx.repo, "hash-object", "-t", "tree", os.DevNull)
			if empty.code == 0 && trimJS(empty.stdout) != "" {
				base = trimJS(empty.stdout)
			}
		}
		resolved.source.Commit, resolved.source.ResolvedBase = sha, base
		resolved.args = []string{base, sha}
		resolved.working = false
	default:
		return fail("error", reviewTurnRejection)
	}
	return resolved, "ok", ""
}
func (ctx *reviewContext) status(source reviewSource, revisionOnly bool) reviewStatus {
	response := reviewEmptyStatus(reviewRevision{State: "ok", Source: source})
	head := ctx.head()
	entries := []reviewEntry{}
	if source.Kind != "commit" {
		var failure string
		entries, failure = ctx.entries()
		if failure != "" {
			response.State, response.Error = "error", failure
			return response
		}
		response.Snapshot = ctx.snapshot(head, entries)
	}
	resolved, state, failure := ctx.resolve(source, head)
	response.Source, response.State, response.Error = resolved.source, state, failure
	if state != "ok" {
		return response
	}
	response.Snapshot = reviewComparisonSnapshot(response.Snapshot, resolved.source)
	if revisionOnly {
		return response
	}
	files, failure := ctx.collect(resolved, entries)
	if failure != "" {
		response.State, response.Error = "error", failure
		return response
	}
	response.Files = files
	for _, file := range files {
		response.Totals.Additions += file.Additions
		response.Totals.Deletions += file.Deletions
		if file.Status == "untracked" {
			response.Untracked = append(response.Untracked, file.Path)
		}
	}
	response.Totals.Files = len(files)
	return response
}
