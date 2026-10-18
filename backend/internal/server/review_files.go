package server

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type reviewStat struct {
	path, old            string
	additions, deletions int
	binary               bool
}
type reviewName struct{ letter, old string }

func reviewNumstat(output string) []reviewStat {
	result := []reviewStat{}
	parts := strings.Split(output, "\x00")
	for i := 0; i < len(parts); i++ {
		fields := strings.SplitN(parts[i], "\t", 3)
		if len(fields) != 3 {
			continue
		}
		a, _ := strconv.Atoi(fields[0])
		d, _ := strconv.Atoi(fields[1])
		stat := reviewStat{path: normalizeWorkspacePath(fields[2]), additions: a, deletions: d, binary: fields[0] == "-" && fields[1] == "-"}
		if fields[2] == "" {
			if i+2 >= len(parts) {
				break
			}
			stat.old, stat.path = normalizeWorkspacePath(parts[i+1]), normalizeWorkspacePath(parts[i+2])
			i += 2
		}
		if stat.path != "" {
			result = append(result, stat)
		}
	}
	return result
}
func reviewNames(output string) map[string]reviewName {
	result := map[string]reviewName{}
	parts := strings.Split(output, "\x00")
	for i := 0; i < len(parts); i++ {
		if parts[i] == "" {
			continue
		}
		name := reviewName{letter: parts[i][:1]}
		path := ""
		if name.letter == "R" || name.letter == "C" {
			if i+2 >= len(parts) {
				break
			}
			name.old, path = normalizeWorkspacePath(parts[i+1]), normalizeWorkspacePath(parts[i+2])
			i += 2
		} else {
			if i+1 >= len(parts) {
				break
			}
			path = normalizeWorkspacePath(parts[i+1])
			i++
		}
		if path != "" {
			result[path] = name
		}
	}
	return result
}
func reviewStatusLetter(letter byte) string {
	switch letter {
	case 'M':
		return "modified"
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "type_changed"
	case 'U':
		return "conflicted"
	case '?':
		return "untracked"
	}
	return "unknown"
}
func reviewFileKind(resolved reviewResolved, entry *reviewEntry, letter string) string {
	if entry != nil {
		if entry.conflicted {
			return "conflicted"
		}
		if entry.untracked {
			return "untracked"
		}
		switch resolved.source.Kind {
		case "unstaged":
			return reviewStatusLetter(entry.code[1])
		case "staged":
			return reviewStatusLetter(entry.code[0])
		case "uncommitted":
			return workspaceStatusKind(entry.code)
		}
	}
	if letter != "" {
		return reviewStatusLetter(letter[0])
	}
	return "unknown"
}
func (ctx *reviewContext) collect(resolved reviewResolved, entries []reviewEntry) ([]reviewFile, string) {
	args := append([]string{"diff"}, resolved.args...)
	numArgs := append(append([]string{}, args...), "--numstat", "-z", "--find-renames", "--find-copies", "--")
	nameArgs := append(append([]string{}, args...), "--name-status", "-z", "--find-renames", "--find-copies", "--")
	num, names := ctx.git(ctx.repo, numArgs...), ctx.git(ctx.repo, nameArgs...)
	if num.code != 0 {
		return nil, workspaceGitError("Failed to read review stats", ctx.repo, num, numArgs...)
	}
	if names.code != 0 {
		return nil, workspaceGitError("Failed to read review file list", ctx.repo, names, nameArgs...)
	}
	byPath := map[string]*reviewEntry{}
	for i := range entries {
		byPath[entries[i].path] = &entries[i]
	}
	nameByPath := reviewNames(names.stdout)
	files := map[string]reviewFile{}
	for _, stat := range reviewNumstat(num.stdout) {
		path, inside := ctx.rebase(stat.path)
		if !inside {
			continue
		}
		var entry *reviewEntry
		if resolved.working {
			entry = byPath[path]
		}
		name := nameByPath[stat.path]
		old := stat.old
		if old == "" {
			old = name.old
		}
		oldPath := ""
		if old != "" {
			oldPath, _ = ctx.rebase(old)
		}
		file := reviewFile{Path: path, OldPath: oldPath, Status: reviewFileKind(resolved, entry, name.letter), Additions: stat.additions, Deletions: stat.deletions, Binary: stat.binary}
		if entry != nil {
			file.Staged, file.Unstaged, file.Conflicted = entry.staged, entry.unstaged, entry.conflicted
		}
		files[path] = file
	}
	if resolved.working {
		for _, entry := range entries {
			if _, exists := files[entry.path]; exists {
				continue
			}
			if entry.conflicted {
				files[entry.path] = reviewFile{Path: entry.path, OldPath: entry.oldPath, Status: "conflicted", Conflicted: true}
				continue
			}
			if entry.untracked && resolved.includeUntracked {
				add, binary, truncated := reviewUntrackedStats(entry.absolute)
				files[entry.path] = reviewFile{Path: entry.path, Status: "untracked", Additions: add, Binary: binary, Unstaged: true, StatsTruncated: truncated}
			}
		}
	}
	result := make([]reviewFile, 0, len(files))
	for _, file := range files {
		result = append(result, file)
	}
	comparator := collate.New(language.English)
	sort.SliceStable(result, func(i, j int) bool { return comparator.CompareString(result[i].Path, result[j].Path) < 0 })
	return result, ""
}

type reviewTarget struct{ relative, absolute, canonical, repo string }

func reviewMetadataPath(relative string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(relative), "/") {
		if vcsDirectory(segment) {
			return true
		}
	}
	return false
}
func reviewForbidden(message string) error { return &apiError{403, "FORBIDDEN", message} }
func (ctx *reviewContext) target(requested string, historical bool) (reviewTarget, error) {
	if trimJS(requested) == "" {
		return reviewTarget{}, badRequest("path is required")
	}
	absolute := normalizeDriveRoot(requested)
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(ctx.root, absolute)
	}
	absolute = filepath.Clean(absolute)
	if !sameOrInsideFilesystemPath(absolute, ctx.root) {
		return reviewTarget{}, reviewForbidden("Path is outside workspace: " + requested)
	}
	relative, err := filepath.Rel(ctx.root, absolute)
	if err != nil {
		return reviewTarget{}, err
	}
	relative = normalizeWorkspacePath(relative)
	if relative == "" {
		return reviewTarget{}, fmt.Errorf("Path is not a file inside the workspace: %s", requested)
	}
	if reviewMetadataPath(relative) {
		return reviewTarget{}, reviewForbidden("Path is inside version-control metadata: " + requested)
	}
	canonical := filepath.Join(ctx.canonicalRoot, filepath.FromSlash(relative))
	if !historical {
		stat, err := os.Lstat(absolute)
		if err != nil && !workspaceMissing(err) {
			return reviewTarget{}, fmt.Errorf("%s", workspaceFSError("Failed to stat review path", absolute, err))
		}
		if err == nil && stat.Mode()&os.ModeSymlink != 0 {
			return reviewTarget{}, fmt.Errorf("Path is a symbolic link and is not reviewable: %s", requested)
		}
		probe := absolute
		suffix := []string{}
		for {
			base, err := filepath.EvalSymlinks(probe)
			if err == nil {
				canonical = filepath.Join(append([]string{base}, suffix...)...)
				break
			}
			if !workspaceMissing(err) {
				return reviewTarget{}, fmt.Errorf("%s", workspaceFSError("Failed to canonicalize review path", absolute, err))
			}
			parent := filepath.Dir(probe)
			if parent == probe {
				return reviewTarget{}, err
			}
			suffix = append([]string{filepath.Base(probe)}, suffix...)
			probe = parent
		}
		if !sameOrInsideFilesystemPath(canonical, ctx.canonicalRoot) {
			return reviewTarget{}, reviewForbidden("Path is outside workspace: " + requested)
		}
		canonicalRelative, err := filepath.Rel(ctx.canonicalRoot, canonical)
		if err != nil {
			return reviewTarget{}, err
		}
		if reviewMetadataPath(canonicalRelative) {
			return reviewTarget{}, reviewForbidden("Path is inside version-control metadata: " + requested)
		}
	}
	repo, err := filepath.Rel(ctx.repo, canonical)
	if err != nil {
		return reviewTarget{}, err
	}
	if repo == "." || !sameOrInsideFilesystemPath(canonical, ctx.repo) {
		return reviewTarget{}, reviewForbidden("Path is outside workspace: " + requested)
	}
	return reviewTarget{relative: relative, absolute: absolute, canonical: canonical, repo: normalizeWorkspacePath(repo)}, nil
}
func reviewUntrackedStats(path string) (int, bool, bool) {
	stat, err := os.Stat(path)
	if err != nil || !stat.Mode().IsRegular() {
		return 0, false, false
	}
	if stat.Size() > 32*1024*1024 {
		return 0, false, true
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, false, false
	}
	defer file.Close()
	chunk := make([]byte, 256*1024)
	previous, total, lines := -1, 0, 0
	for {
		n, err := file.Read(chunk)
		if n > 0 {
			view := chunk[:n]
			if total == 0 && bytes.IndexByte(view, 0) >= 0 {
				return 0, true, false
			}
			for _, b := range view {
				if previous == '\r' && b != '\n' {
					lines++
				}
				if b == '\n' {
					lines++
				}
				previous = int(b)
			}
			total += n
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, false, false
		}
	}
	if previous == '\r' {
		lines++
	} else if total > 0 && previous != '\n' {
		lines++
	}
	return lines, false, false
}
func reviewQuote(path string) string {
	if !strings.ContainsAny(path, "\x00\x01\x02\x03\x04\x05\x06\x07\b\t\n\v\f\r\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f \"\\") {
		return path
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range path {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 32 {
				fmt.Fprintf(&b, "\\%03o", r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
func reviewSynthetic(path, repo string) (*string, bool, bool, int64, string, string) {
	stat, err := os.Stat(path)
	if workspaceMissing(err) || err == nil && !stat.Mode().IsRegular() {
		return nil, false, false, 0, "missing", ""
	}
	if err != nil {
		return nil, false, false, 0, "error", workspaceFSError("Failed to stat review path", path, err)
	}
	mode := "100644"
	if stat.Mode().Perm()&0111 != 0 {
		mode = "100755"
	}
	header := "diff --git " + reviewQuote("a/"+repo) + " " + reviewQuote("b/"+repo) + "\nnew file mode " + mode + "\n"
	if stat.Size() > 1024*1024 {
		return &header, false, true, stat.Size(), "ok", ""
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, false, false, 0, "error", workspaceFSError("Failed to read untracked file", path, err)
	}
	if bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content) {
		header += "Binary files /dev/null and b/" + repo + " differ\n"
		return &header, true, false, 0, "ok", ""
	}
	if len(content) > 0 {
		lines := strings.Split(string(content), "\n")
		if bytes.HasSuffix(content, []byte("\n")) {
			lines = lines[:len(lines)-1]
		}
		var patch strings.Builder
		patch.Grow(len(content) + len(lines) + len(header) + 128)
		patch.WriteString(header)
		patch.WriteString("--- /dev/null\n+++ " + reviewQuote("b/"+repo) + fmt.Sprintf("\n@@ -0,0 +1,%d @@\n", len(lines)))
		for _, line := range lines {
			patch.WriteByte('+')
			patch.WriteString(line)
			patch.WriteByte('\n')
		}
		if !bytes.HasSuffix(content, []byte("\n")) {
			patch.WriteString("\\ No newline at end of file\n")
		}
		header = patch.String()
	}
	return &header, false, false, 0, "ok", ""
}
func (ctx *reviewContext) diff(source reviewSource, path, oldPath string) (reviewDiff, error) {
	response := reviewDiff{reviewRevision: reviewRevision{State: "ok", Source: source}, Path: filepath.ToSlash(path)}
	target, err := ctx.target(path, source.Kind == "commit")
	if err != nil {
		return response, err
	}
	response.Path = target.relative
	var old *reviewTarget
	if oldPath != "" {
		value, err := ctx.target(oldPath, source.Kind == "commit")
		if err != nil {
			return response, err
		}
		old = &value
	}
	head := ctx.head()
	entries := []reviewEntry{}
	if source.Kind != "commit" {
		var failure string
		entries, failure = ctx.entries()
		if failure != "" {
			response.State, response.Error = "error", failure
			return response, nil
		}
		response.Snapshot = ctx.snapshot(head, entries)
	}
	resolved, state, failure := ctx.resolve(source, head)
	response.Source, response.State, response.Error = resolved.source, state, failure
	if state != "ok" {
		return response, nil
	}
	response.Snapshot = reviewComparisonSnapshot(response.Snapshot, resolved.source)
	var entry *reviewEntry
	for i := range entries {
		if entries[i].path == target.relative {
			entry = &entries[i]
			break
		}
	}
	if resolved.working && entry != nil && entry.untracked {
		if !resolved.includeUntracked {
			response.State = "missing"
			return response, nil
		}
		text, binary, truncated, size, state, failure := reviewSynthetic(entry.absolute, entry.repoPath)
		response.State, response.Error = state, failure
		if state == "ok" {
			response.Diff, response.Binary, response.Truncated, response.Bytes = text, &binary, truncated, size
		}
		return response, nil
	}
	paths := []string{reviewLiteral(target.repo)}
	knownOld := ""
	if old != nil {
		knownOld = old.repo
	} else if resolved.working && entry != nil {
		knownOld = entry.repoOldPath
	}
	if knownOld != "" && knownOld != target.repo {
		paths = append(paths, reviewLiteral(knownOld))
	}
	args := append([]string{"diff"}, resolved.args...)
	args = append(args, "--no-ext-diff", "--find-renames", "--find-copies", "--")
	args = append(args, paths...)
	result := ctx.git(ctx.repo, args...)
	if result.code != 0 {
		response.State, response.Error = "error", workspaceGitError("Failed to read review diff", ctx.repo, result, args...)
		return response, nil
	}
	if trimJS(result.stdout) == "" {
		response.State = "missing"
		return response, nil
	}
	response.Diff = &result.stdout
	binary := reviewBinaryPatch.MatchString(result.stdout)
	response.Binary = &binary
	if knownOld != "" {
		response.OldPath, _ = ctx.rebase(knownOld)
	}
	return response, nil
}

var reviewBinaryPatch = regexp.MustCompile(`(?m)^(Binary files |GIT binary patch)`)
var reviewPatchTokens = regexp.MustCompile(`"(?:\\.|[^"\\])*"|[^ ]+`)

func reviewStripPatchPrefix(value string) string {
	if len(value) > 1 && value[0] == '"' && value[len(value)-1] == '"' {
		if decoded, err := strconv.Unquote(value); err == nil {
			value = decoded
		}
	}
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		return value[slash+1:]
	}
	return value
}
func reviewPatchPaths(patch string) []string {
	result := []string{}
	seen := map[string]bool{}
	add := func(value string) {
		if value == "" || value == "/dev/null" {
			return
		}
		path := reviewStripPatchPrefix(value)
		if path != "" && !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			add(strings.SplitN(line[4:], "\t", 2)[0])
		} else if strings.HasPrefix(line, "diff --git ") {
			parts := reviewPatchTokens.FindAllString(strings.TrimPrefix(line, "diff --git "), -1)
			if len(parts) == 2 {
				add(parts[0])
				add(parts[1])
			}
		}
	}
	return result
}
