package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/server/ignorepattern"
	"golang.org/x/text/cases"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type filesystemSearch struct{ cancel context.CancelFunc }
type searchIgnoreContext struct {
	base    string
	matcher *ignorepattern.Matcher
}
type filesystemScanOptions struct {
	timeout                  time.Duration
	maxDirectories, maxFiles int
	query                    string
}

func normalizeSearchText(value string) string {
	value = strings.ReplaceAll(trimJS(value), `\`, "/")
	value = strings.TrimLeft(value, "@")
	if strings.HasPrefix(value, "./") {
		value = strings.TrimLeft(value[1:], "/")
	}
	value = strings.TrimRight(value, "/")
	return cases.Lower(language.Und).String(value)
}
func normalizedRelativePath(value string) string {
	value = strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(value, "./") {
		value = strings.TrimLeft(value[1:], "/")
	}
	return value
}
func relativeInsideSearchRoot(value string) bool {
	return value != "" && value != "." && !strings.HasPrefix(value, "../") && !filepath.IsAbs(value)
}
func fuzzyFilesystemScore(value, query string) int {
	input, wanted := utf16.Encode([]rune(value)), utf16.Encode([]rune(query))
	index, run, score := 0, 0, 0
	for i, char := range input {
		if index >= len(wanted) {
			break
		}
		if char != wanted[index] {
			run = 0
			continue
		}
		boost := 0
		if i == 0 || strings.ContainsRune("/-_. ", rune(input[i-1])) {
			boost = 3
		}
		run++
		score += 2 + boost + min(run, 4)
		index++
	}
	if index == len(wanted) {
		return score
	}
	return 0
}
func filesystemEntryScore(name, relative, query string, directory bool) int {
	normalizedName, normalizedPath := normalizeSearchText(name), normalizeSearchText(relative)
	withoutExtension := normalizedPath
	if dot := strings.LastIndex(normalizedPath, "."); dot > strings.LastIndex(normalizedPath, "/") && dot+1 < len(normalizedPath) {
		withoutExtension = normalizedPath[:dot]
	}
	boost := 0
	if directory {
		boost = 4
	}
	penalty := min(strings.Count(relative, "/"), 8) * 2
	for _, condition := range []struct {
		matched bool
		score   int
	}{
		{normalizedPath == query, 150}, {withoutExtension == query, 144}, {strings.HasPrefix(normalizedPath, query+"/"), 136},
		{strings.HasPrefix(normalizedPath, query), 112}, {normalizedName == query, 96}, {strings.HasPrefix(normalizedName, query), 88},
		{strings.Contains(normalizedName, query), 72}, {strings.Contains(normalizedPath, query), 60},
	} {
		if condition.matched {
			return condition.score + boost - penalty
		}
	}
	if score := fuzzyFilesystemScore(normalizedName, query); score > 0 {
		return 44 + score + boost - penalty
	}
	if score := fuzzyFilesystemScore(normalizedPath, query); score > 0 {
		return 28 + score + boost - penalty
	}
	return 0
}
func filesystemFallbackMatch(relative, query string) bool {
	if filesystemEntryScore(path.Base(relative), relative, query, false) > 0 {
		return true
	}
	for directory := path.Dir(relative); directory != "."; directory = path.Dir(directory) {
		if filesystemEntryScore(path.Base(directory), directory, query, true) > 0 {
			return true
		}
		if path.Dir(directory) == directory {
			break
		}
	}
	return false
}
func vcsDirectory(value string) bool {
	switch strings.ToLower(value) {
	case ".git", ".svn", ".hg", ".bzr", ".jj", ".sl":
		return true
	}
	return false
}
func searchPlatform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}
func loadSearchIgnore(directories []string, includeGitignore bool) (*ignorepattern.Matcher, error) {
	names := []string{".ignore", ".rgignore"}
	if includeGitignore {
		names = append([]string{".gitignore"}, names...)
	}
	var matcher *ignorepattern.Matcher
	seen := map[string]bool{}
	for _, directory := range directories {
		if seen[directory] {
			continue
		}
		seen[directory] = true
		for _, name := range names {
			data, err := os.ReadFile(filepath.Join(directory, name))
			if err != nil {
				continue
			}
			if matcher == nil {
				matcher, err = ignorepattern.New(searchPlatform())
				if err != nil {
					return nil, err
				}
			}
			if err := matcher.Add(string(data)); err != nil {
				return nil, err
			}
		}
	}
	return matcher, nil
}
func ignoredBySearchContexts(contexts []searchIgnoreContext, relative string, directory bool) (bool, error) {
	ignored := false
	for _, context := range contexts {
		scoped := relative
		if context.base != "" {
			var err error
			scoped, err = filepath.Rel(filepath.FromSlash(context.base), filepath.FromSlash(relative))
			if err != nil {
				continue
			}
			scoped = filepath.ToSlash(scoped)
		}
		if !relativeInsideSearchRoot(scoped) {
			continue
		}
		if directory {
			scoped += "/"
		}
		result, err := context.matcher.Test(scoped)
		if err != nil {
			return false, err
		}
		if result.Ignored {
			ignored = true
		}
		if result.Unignored {
			ignored = false
		}
	}
	return ignored, nil
}
func filesUsingFilesystem(ctx context.Context, root string, respect bool, options filesystemScanOptions) ([]string, error) {
	deadline := time.Now().Add(options.timeout)
	files := []string{}
	type directory struct {
		absolute, relative string
		contexts           []searchIgnoreContext
	}
	directories := []directory{{absolute: root}}
	collator := collate.New(language.English)
	for index := 0; index < len(directories) && index < options.maxDirectories && len(files) < options.maxFiles && time.Now().Before(deadline); index++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current := directories[index]
		entries, err := os.ReadDir(current.absolute)
		if err != nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sort.SliceStable(entries, func(a, b int) bool {
			if entries[a].IsDir() != entries[b].IsDir() {
				return entries[a].IsDir()
			}
			return collator.CompareString(entries[a].Name(), entries[b].Name()) < 0
		})
		contexts := current.contexts
		matcher, err := loadSearchIgnore([]string{current.absolute}, respect)
		if err != nil {
			return nil, err
		}
		if matcher != nil {
			contexts = append(append([]searchIgnoreContext{}, contexts...), searchIgnoreContext{current.relative, matcher})
		}
		for _, entry := range entries {
			relative := normalizedRelativePath(path.Join(current.relative, entry.Name()))
			ignored, err := ignoredBySearchContexts(contexts, relative, entry.IsDir())
			if err != nil {
				return nil, err
			}
			if entry.IsDir() {
				if !vcsDirectory(entry.Name()) && !ignored {
					directories = append(directories, directory{filepath.Join(current.absolute, entry.Name()), relative, contexts})
				}
				continue
			}
			if !entry.Type().IsRegular() || ignored || options.query != "" && !filesystemFallbackMatch(relative, options.query) {
				continue
			}
			files = append(files, relative)
			if len(files) >= options.maxFiles {
				break
			}
		}
	}
	return files, nil
}
func nearestSearchGitRoot(directory string) string {
	for {
		if stat, err := os.Stat(filepath.Join(directory, ".git")); err == nil && (stat.IsDir() || stat.Mode().IsRegular()) {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return ""
		}
		directory = parent
	}
}
func executableUsingPath(name string, environment map[string]string) (string, error) {
	// Resolve against the same frozen process environment passed to the child.
	suffixes := []string{""}
	if runtime.GOOS == "windows" {
		extensions := environment["PATHEXT"]
		if extensions == "" {
			extensions = ".COM;.EXE;.BAT;.CMD"
		}
		suffixes = strings.Split(extensions, ";")
	}
	for _, directory := range filepath.SplitList(environment["PATH"]) {
		if directory == "" {
			continue
		}
		for _, suffix := range suffixes {
			candidate := filepath.Join(directory, name+suffix)
			stat, err := os.Stat(candidate)
			if err == nil && stat.Mode().IsRegular() && (runtime.GOOS == "windows" || stat.Mode()&0111 != 0) {
				absolute, err := filepath.Abs(candidate)
				if err == nil {
					return absolute, nil
				}
			}
		}
	}
	return "", fmt.Errorf("executable %s not found in frozen PATH", name)
}
func (server *Server) searchGitCommand(ctx context.Context, root string, args ...string) (string, error) {
	environment := map[string]string{}
	if server.options.Environment != nil {
		for name, value := range server.options.Environment() {
			environment[name] = value
		}
	}
	environment["HOME"], environment["USERPROFILE"] = server.options.HomeDir, server.options.HomeDir
	environment["CLAUDE_CONFIG_DIR"], environment["GIT_OPTIONAL_LOCKS"] = server.options.ConfigDir, "0"
	executable, err := executableUsingPath("git", environment)
	if err != nil {
		return "", err
	}
	commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(commandContext, executable, args...)
	command.Dir = root
	for name, value := range environment {
		command.Env = append(command.Env, name+"="+value)
	}
	data, err := command.Output()
	return string(data), err
}
func (server *Server) filesUsingGit(ctx context.Context, root string, respect bool) ([]string, error) {
	repository := server.findCachedGitRoot(root)
	if repository == "" {
		return nil, nil
	}
	tracked, err := server.searchGitCommand(ctx, repository, "-c", "core.quotepath=false", "ls-files", "--recurse-submodules")
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, nil
	}
	args := []string{"-c", "core.quotepath=false", "ls-files", "--others"}
	if respect {
		args = append(args, "--exclude-standard")
	}
	untracked, err := server.searchGitCommand(ctx, repository, args...)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		untracked = ""
	}
	matcher, err := loadSearchIgnore([]string{repository, root}, false)
	if err != nil {
		return nil, err
	}
	files := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(trimJS(tracked+"\n"+untracked), "\n") {
		line = trimJS(line)
		if line == "" {
			continue
		}
		relative, err := filepath.Rel(root, filepath.Join(repository, line))
		if err != nil {
			continue
		}
		relative = normalizedRelativePath(relative)
		if !relativeInsideSearchRoot(relative) || seen[relative] {
			continue
		}
		if matcher != nil {
			result, err := matcher.Test(relative)
			if err != nil {
				return nil, err
			}
			if result.Ignored {
				continue
			}
		}
		seen[relative] = true
		files = append(files, relative)
	}
	return files, nil
}
func sourceSearchSliceLength(raw string, length int) int {
	if raw == "" {
		raw = "200"
	}
	prefix := parseIntPrefix.FindString(trimJS(raw))
	if prefix == "" {
		return 0
	}
	value, err := strconv.ParseFloat(prefix, 64)
	if err != nil && !math.IsInf(value, 0) {
		return 0
	}
	value = math.Min(value, 200)
	if value < 0 {
		value = math.Max(float64(length)+value, 0)
	}
	value = math.Min(value, float64(length))
	return int(value)
}
func (server *Server) filesystemSearchEntries(parent context.Context, root, query string, includeFiles, includeDirectories bool, sliceLength func(int) int) ([]filesystemEntry, error) {
	if server.options.RespectGitignore == nil {
		return nil, unsupported("filesystem search initial/global settings binding")
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(server.ctx, cancel)
	defer stop()
	defer cancel()
	search := &filesystemSearch{cancel: cancel}
	server.mu.Lock()
	if server.filesystemSearches == nil {
		server.filesystemSearches = map[string]*filesystemSearch{}
	}
	if old := server.filesystemSearches[root]; old != nil {
		old.cancel()
	}
	server.filesystemSearches[root] = search
	server.mu.Unlock()
	defer func() {
		server.mu.Lock()
		if server.filesystemSearches[root] == search {
			delete(server.filesystemSearches, root)
		}
		server.mu.Unlock()
	}()
	normalizedQuery := normalizeSearchText(query)
	entries := []filesystemEntry{}
	if normalizedQuery != "" {
		respect, err := server.options.RespectGitignore(ctx)
		if err != nil {
			return nil, err
		}
		files, err := server.filesUsingGit(ctx, root, respect)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			files, err = filesUsingFilesystem(ctx, root, respect, filesystemScanOptions{timeout: 2 * time.Second, maxDirectories: 5000, maxFiles: 20000, query: normalizedQuery})
			if err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidates := map[string]filesystemEntry{}
		order := []string{}
		add := func(relative string, directory bool) {
			if _, exists := candidates[relative]; exists {
				return
			}
			candidates[relative] = filesystemEntry{Name: path.Base(relative), Path: filepath.Join(root, filepath.FromSlash(relative)), IsDirectory: directory, RelativePath: relative}
			order = append(order, relative)
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			relative := normalizedRelativePath(file)
			if !relativeInsideSearchRoot(relative) {
				continue
			}
			for directory := path.Dir(relative); includeDirectories && directory != "."; directory = path.Dir(directory) {
				add(directory, true)
				if path.Dir(directory) == directory {
					break
				}
			}
			if includeFiles {
				add(relative, false)
			}
		}
		type scored struct {
			entry filesystemEntry
			score int
		}
		scores := []scored{}
		for _, relative := range order {
			entry := candidates[relative]
			score := filesystemEntryScore(entry.Name, relative, normalizedQuery, entry.IsDirectory)
			if score > 0 {
				scores = append(scores, scored{entry, score})
			}
		}
		collator := collate.New(language.English)
		sort.SliceStable(scores, func(a, b int) bool {
			if scores[a].score != scores[b].score {
				return scores[a].score > scores[b].score
			}
			left, right := scores[a].entry, scores[b].entry
			if left.IsDirectory != right.IsDirectory {
				return left.IsDirectory
			}
			if da, db := strings.Count(left.RelativePath, "/"), strings.Count(right.RelativePath, "/"); da != db {
				return da < db
			}
			return collator.CompareString(left.RelativePath, right.RelativePath) < 0
		})
		for _, value := range scores[:sliceLength(len(scores))] {
			entries = append(entries, value.entry)
		}
	}
	return entries, nil
}

func (server *Server) filesystemSearchHTTP(writer http.ResponseWriter, request *http.Request, root string) error {
	query := request.URL.Query().Get("search")
	entries, err := server.filesystemSearchEntries(requestSignalContext(request), root, query, request.URL.Query().Get("includeFiles") == "true", true, func(length int) int { return sourceSearchSliceLength(request.URL.Query().Get("maxResults"), length) })
	if err != nil {
		return err
	}
	writeJSON(writer, 200, map[string]any{"currentPath": root, "parentPath": filepath.Dir(root), "entries": entries, "query": query})
	return nil
}
