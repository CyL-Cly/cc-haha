package searchapi

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func directProject(v json.RawMessage) bool {
	if !isString(v) {
		return false
	}
	s := str(v)
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`) && filepath.Base(s) == s
}
func (s *Service) SearchSessions(ctx context.Context, query string, o protocol.JSONObject) (SessionOutput, error) {
	out := SessionOutput{Results: []SessionResult{}}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	query = trim(query)
	if query == "" {
		return out, bad("Search query is required")
	}
	sensitive := truthy(o.Get("caseSensitive"))
	limit := number(o.Get("limit"), 50)
	matchesPer := number(o.Get("matchesPerSession"), 5)
	projects := filepath.Join(s.o.ConfigDir, "projects")
	projectRaw := o.Get("project")
	var project *string
	if len(projectRaw) > 0 {
		if !directProject(projectRaw) {
			return out, bad("Invalid project filter")
		}
		project = ptr(str(projectRaw))
	}
	root := projects
	if project != nil {
		root = filepath.Join(root, *project)
	}
	info, e := os.Stat(root)
	if e != nil || !info.IsDir() {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, nil
	}
	rootSnapshot, rootSnapshotError := nativeSnapshot(root)
	after, before := math.Inf(-1), math.Inf(1)
	var afterOption, beforeOption *float64
	datesValid := true
	for _, entry := range []struct {
		field  string
		value  *float64
		target **float64
	}{{"modifiedAfter", &after, &afterOption}, {"modifiedBefore", &before, &beforeOption}} {
		if !truthy(o.Get(entry.field)) {
			continue
		}
		if !isString(o.Get(entry.field)) {
			*entry.value = math.NaN()
			datesValid = false
			continue
		}
		ms, valid, err := s.o.ParseDate(str(o.Get(entry.field)))
		if err != nil {
			return out, err
		}
		if !valid {
			*entry.value = math.NaN()
			datesValid = false
		} else {
			*entry.value = ms
			*entry.target = entry.value
		}
	}
	if datesValid {
		maxFiles := 60.0
		indexed, e := s.o.Content.Query(ctx, query, localindex.ContentQueryOptions{Project: project, ModifiedAfterMs: afterOption, ModifiedBeforeMs: beforeOption, Limit: &maxFiles, MatchesPerSession: actualNumber(o.Get("matchesPerSession")), CaseSensitive: sensitive})
		if e != nil {
			return out, e
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if indexed != nil && s.verifyIndexed(ctx, indexed, projects, query, sensitive) {
			return s.resultsFromIndex(ctx, indexed, query, sensitive, limit, matchesPer)
		}
	}
	var narrowed []FilterCandidate
	filterAfter, filterBefore := afterOption, beforeOption
	if truthy(o.Get("modifiedAfter")) {
		filterAfter = &after
	}
	if truthy(o.Get("modifiedBefore")) {
		filterBefore = &before
	}
	if s.o.GetCandidatesForFilters != nil && (project != nil || afterOption != nil || beforeOption != nil) {
		narrowed, e = s.o.GetCandidatesForFilters(ctx, localindex.ContentQueryOptions{Project: project, ModifiedAfterMs: filterAfter, ModifiedBeforeMs: filterBefore})
		if e != nil {
			return out, e
		}
	}
	var candidates []string
	if narrowed != nil {
		paths := []string{}
		for _, candidate := range narrowed {
			paths = append(paths, candidate.Path)
		}
		candidates, e = s.candidatePaths(ctx, paths, query, sensitive)
	} else {
		candidates, e = s.candidateFiles(ctx, root, query, sensitive)
	}
	if e != nil {
		return out, e
	}
	if narrowed != nil {
		refreshed, refreshErr := s.o.GetCandidatesForFilters(ctx, localindex.ContentQueryOptions{Project: project, ModifiedAfterMs: filterAfter, ModifiedBeforeMs: filterBefore})
		beforeDirectory, beforeErr := rootSnapshot, rootSnapshotError
		afterDirectory, afterErr := nativeSnapshot(root)
		if refreshErr != nil || refreshed == nil || !sameCandidates(narrowed, refreshed) || beforeErr != nil || afterErr != nil || !sameSnapshot(beforeDirectory, afterDirectory) {
			narrowed = nil
			candidates, e = s.candidateFiles(ctx, root, query, sensitive)
			if e != nil {
				return out, e
			}
		} else {
			narrowed = refreshed
		}
	}
	type ranked struct {
		path  string
		mtime float64
	}
	rankedFiles := []ranked{}
	for _, p := range candidates {
		mtime := 0.0
		if info, e := os.Stat(p); e == nil {
			mtime = float64(info.ModTime().Unix())*1000 + float64(info.ModTime().Nanosecond())/1e6
		}
		if project != nil && filepath.Base(filepath.Dir(p)) != *project || !(mtime >= after && mtime <= before) {
			continue
		}
		rankedFiles = append(rankedFiles, ranked{p, mtime})
	}
	sort.SliceStable(rankedFiles, func(a, b int) bool { return rankedFiles[a].mtime > rankedFiles[b].mtime })
	if len(rankedFiles) > 60 {
		rankedFiles = rankedFiles[:60]
		out.Truncated = true
	}
	paths := []string{}
	for _, r := range rankedFiles {
		paths = append(paths, r.path)
	}
	var metadata map[string]Metadata
	if narrowed != nil {
		metadata = map[string]Metadata{}
		for _, candidate := range narrowed {
			metadata[candidate.Path] = candidate.Metadata
		}
	} else {
		metadata, e = s.o.Metadata(ctx, paths)
	}
	if e != nil {
		return out, e
	}
	matchedLines, e := s.matchedLines(ctx, paths, query, sensitive)
	if e != nil {
		return out, e
	}
	for _, item := range rankedFiles {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		lines := matchedLines[item.path]
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if len(lines) == 0 {
			continue
		}
		targeted, e := s.o.ReadEntriesAtLines(ctx, item.path, lines)
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if e != nil {
			return out, e
		}
		var entries []EntryLine
		if targeted != nil {
			entries = targeted.Entries
		} else {
			entries, e = readMatchingEntries(ctx, item.path, lines)
			if e != nil {
				if ctx.Err() != nil {
					return out, ctx.Err()
				}
				continue
			}
		}
		matches, count := extractMatches(entries, query, sensitive, matchesPer)
		if count == 0 {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(item.path), ".jsonl")
		result := SessionResult{id, id, filepath.Base(filepath.Dir(item.path)), nil, iso(max(0, item.mtime)), count, matches}
		if m, ok := metadata[item.path]; ok && targeted != nil {
			result.Title = m.Title
			result.ProjectPath = m.ProjectPath
			result.WorkDir = m.WorkDir
			result.ModifiedAt = m.ModifiedAt
		}
		out.Results = append(out.Results, result)
	}
	sort.SliceStable(out.Results, func(a, b int) bool { return out.Results[a].ModifiedAt > out.Results[b].ModifiedAt })
	if float64(len(out.Results)) > limit {
		out.Results = out.Results[:sliceEnd(len(out.Results), limit)]
		out.Truncated = true
	}
	return out, nil
}
func sameCandidates(a, b []FilterCandidate) bool {
	if len(a) != len(b) {
		return false
	}
	values := map[string]*localindex.FileSnapshot{}
	for _, candidate := range b {
		values[filepath.Clean(candidate.Path)] = candidate.Metadata.SourceSnapshot
	}
	for _, candidate := range a {
		snapshot, exists := values[filepath.Clean(candidate.Path)]
		if !exists {
			return false
		}
		left := candidate.Metadata.SourceSnapshot
		if left == nil && snapshot == nil {
			continue
		}
		if left == nil || snapshot == nil || !sameSnapshot(*left, *snapshot) {
			return false
		}
	}
	return true
}
func batchPaths(paths []string) [][]string {
	batches := [][]string{}
	batch := []string{}
	chars := 0
	for _, path := range paths {
		length := len(utf16.Encode([]rune(path))) + 1
		if len(batch) > 0 && (len(batch) >= 128 || chars+length > 24*1024) {
			batches = append(batches, batch)
			batch = nil
			chars = 0
		}
		batch = append(batch, path)
		chars += length
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches
}
func escapedLiteral(query string) string {
	value := string(protocol.StringifyValidatedJavaScriptJSON(raw(query)))
	return value[1 : len(value)-1]
}
func candidatePath(record string) string {
	value := strings.TrimSuffix(record, "\r")
	if strings.HasPrefix(value, "{") {
		var object struct {
			Type string `json:"type"`
			Data struct {
				Path struct {
					Text string `json:"text"`
				} `json:"path"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(value), &object) == nil {
			if object.Type != "match" {
				return ""
			}
			return object.Data.Path.Text
		}
	}
	return value
}
func (s *Service) candidatePaths(ctx context.Context, paths []string, query string, sensitive bool) ([]string, error) {
	if rg := s.o.Ripgrep(); rg.Path != "" {
		out := []string{}
		seen := map[string]bool{}
		failed := false
		for _, batch := range batchPaths(paths) {
			args := []string{"-l", "--fixed-strings", "--glob", "*.jsonl"}
			if !sensitive {
				args = append(args, "--ignore-case")
			}
			args = append(args, "--", escapedLiteral(query))
			args = append(args, batch...)
			data, e := s.run(ctx, rg, args)
			if e != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				failed = true
				break
			}
			for _, record := range strings.Split(string(data), "\n") {
				path := candidatePath(record)
				if path != "" {
					absolute, _ := filepath.Abs(path)
					if !seen[absolute] {
						out = append(out, absolute)
						seen[absolute] = true
					}
				}
			}
		}
		if !failed {
			return out, nil
		}
	}
	out := []string{}
	for _, path := range paths {
		lines, e := s.scanLiteral(ctx, path, query, sensitive, true)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if e == nil && len(lines) > 0 {
			out = append(out, path)
		}
	}
	return out, nil
}
func (s *Service) matchedLines(ctx context.Context, paths []string, query string, sensitive bool) (map[string]map[int64]bool, error) {
	if rg := s.o.Ripgrep(); rg.Path != "" {
		out := map[string]map[int64]bool{}
		failed := false
		for _, batch := range batchPaths(paths) {
			args := []string{"--line-number", "--with-filename", "--no-heading", "--only-matching", "--replace", "", "--glob", "*.jsonl"}
			if !sensitive {
				args = append(args, "--ignore-case")
			}
			args = append(args, "--", ".*"+regexp.QuoteMeta(escapedLiteral(query))+".*")
			args = append(args, batch...)
			data, e := s.run(ctx, rg, args)
			if e != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				failed = true
				break
			}
			for _, record := range strings.Split(string(data), "\n") {
				record = strings.TrimSuffix(record, "\r")
				last := strings.LastIndex(record, ":")
				if last < 0 || last != len(record)-1 {
					continue
				}
				record = record[:last]
				split := strings.LastIndex(record, ":")
				if split < 0 {
					continue
				}
				line, e := strconv.ParseInt(record[split+1:], 10, 64)
				if e != nil || line < 1 || line > 9007199254740991 {
					continue
				}
				path, _ := filepath.Abs(record[:split])
				if out[path] == nil {
					out[path] = map[int64]bool{}
				}
				out[path][line] = true
			}
		}
		if !failed {
			return out, nil
		}
	}
	out := map[string]map[int64]bool{}
	for _, path := range paths {
		lines, e := s.scanLiteral(ctx, path, query, sensitive, false)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if e == nil && len(lines) > 0 {
			out[path] = lines
		}
	}
	return out, nil
}
func (s *Service) candidateFiles(ctx context.Context, root, query string, sensitive bool) ([]string, error) {
	if rg := s.o.Ripgrep(); rg.Path != "" {
		args := []string{"-l", "--fixed-strings", "--glob", "*.jsonl"}
		if !sensitive {
			args = append(args, "--ignore-case")
		}
		escaped := string(protocol.StringifyValidatedJavaScriptJSON(raw(query)))
		args = append(args, "--", escaped[1:len(escaped)-1], root)
		data, e := s.run(ctx, rg, args)
		if e == nil {
			paths := []string{}
			for _, line := range strings.Split(string(data), "\n") {
				line = candidatePath(line)
				if line != "" {
					absolute, _ := filepath.Abs(line)
					paths = append(paths, absolute)
				}
			}
			return paths, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	files := []string{}
	var walk func(string) error
	walk = func(dir string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entries, e := readDirectory(dir)
		if e != nil {
			return nil
		}
		for _, entry := range entries {
			p := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				if e := walk(p); e != nil {
					return e
				}
			} else if strings.HasSuffix(entry.Name(), ".jsonl") {
				lines, e := s.scanLiteral(ctx, p, query, sensitive, true)
				if e == nil && len(lines) > 0 {
					files = append(files, p)
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
		}
		return nil
	}
	e := walk(root)
	return files, e
}
func scanLines(ctx context.Context, path string, visit func([]byte, int64) bool) error {
	file, e := os.Open(path)
	if e != nil {
		return e
	}
	defer file.Close()
	block := make([]byte, 64*1024)
	var line []byte
	number := int64(0)
	afterCR := false
	flush := func() bool { number++; proceed := visit(line, number); line = nil; return proceed }
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		count, err := file.Read(block)
		for _, value := range block[:count] {
			if value == '\n' {
				if afterCR {
					afterCR = false
					continue
				}
				if !flush() {
					return nil
				}
				continue
			}
			if value == '\r' {
				if !flush() {
					return nil
				}
				afterCR = true
				continue
			}
			afterCR = false
			line = append(line, value)
		}
		if err == io.EOF {
			if len(line) > 0 {
				flush()
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}
func (s *Service) scanLiteral(ctx context.Context, path, query string, sensitive, stopFirst bool) (map[int64]bool, error) {
	needle := string(protocol.StringifyValidatedJavaScriptJSON(raw(query)))
	needle = needle[1 : len(needle)-1]
	if !sensitive {
		needle = localindex.NormalizeContent(needle)
	}
	lines := map[int64]bool{}
	err := scanLines(ctx, path, func(data []byte, n int64) bool {
		haystack := string(data)
		if !sensitive {
			haystack = localindex.NormalizeContent(haystack)
		}
		if strings.Contains(haystack, needle) {
			lines[n] = true
			return !stopFirst
		}
		return true
	})
	return lines, err
}
func readMatchingEntries(ctx context.Context, path string, lines map[int64]bool) ([]EntryLine, error) {
	entries := []EntryLine{}
	e := scanLines(ctx, path, func(data []byte, n int64) bool {
		if !lines[n] || len(data) == 0 {
			return true
		}
		var entry protocol.JSONObject
		if json.Unmarshal(data, &entry) == nil {
			entries = append(entries, EntryLine{entry, n})
		}
		return true
	})
	return entries, e
}
func extractMatches(entries []EntryLine, query string, sensitive bool, maximum float64) ([]SessionMatch, int64) {
	out := []SessionMatch{}
	count := int64(0)
	needle := query
	if !sensitive {
		needle = localindex.NormalizeContent(needle)
	}
	for _, line := range entries {
		for _, segment := range localindex.ExtractSearchableSegments(line.Entry) {
			haystack := segment.Text
			if !sensitive {
				haystack = localindex.NormalizeContent(haystack)
			}
			if !strings.Contains(haystack, needle) {
				continue
			}
			count++
			if float64(len(out)) < maximum {
				snippet, highlights := buildSnippet(segment.Text, query, sensitive)
				var id, stamp *string
				if isString(line.Entry.Get("uuid")) {
					id = ptr(str(line.Entry.Get("uuid")))
				}
				if isString(line.Entry.Get("timestamp")) {
					stamp = ptr(str(line.Entry.Get("timestamp")))
				}
				out = append(out, SessionMatch{segment.Role, id, line.LineNumber, snippet, highlights, stamp})
			}
		}
	}
	return out, count
}
func (s *Service) resultsFromIndex(ctx context.Context, indexed *localindex.ContentQueryResult, query string, sensitive bool, limit, matchesPer float64) (SessionOutput, error) {
	out := SessionOutput{Results: []SessionResult{}, Truncated: indexed.Truncated}
	paths := []string{}
	for _, row := range indexed.Sessions {
		paths = append(paths, row.OwnerTranscriptPath)
	}
	metadata, _ := s.o.Metadata(ctx, paths)
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	for _, row := range indexed.Sessions {
		matches := []SessionMatch{}
		needle := query
		if !sensitive {
			needle = localindex.NormalizeContent(needle)
		}
		exact := []localindex.ContentMatch{}
		for _, m := range row.Matches {
			body := m.Body
			if !sensitive {
				body = localindex.NormalizeContent(body)
			}
			if strings.Contains(body, needle) {
				exact = append(exact, m)
			}
		}
		exact = exact[:sliceEnd(len(exact), matchesPer)]
		for _, m := range exact {
			snippet, highlights := buildSnippet(m.Body, query, sensitive)
			var stamp *string
			if m.Timestamp != nil && *m.Timestamp != "" {
				stamp = m.Timestamp
			}
			matches = append(matches, SessionMatch{m.Role, m.MessageID, m.LineNumber, snippet, highlights, stamp})
		}
		if len(matches) == 0 {
			continue
		}
		result := SessionResult{row.OwnerSessionID, row.OwnerSessionID, row.ProjectPath, nil, iso(row.ModifiedAtMs), row.MatchCount, matches}
		if m, ok := metadata[row.OwnerTranscriptPath]; ok {
			result.Title = m.Title
			result.ProjectPath = m.ProjectPath
			result.WorkDir = m.WorkDir
			result.ModifiedAt = m.ModifiedAt
		}
		out.Results = append(out.Results, result)
	}
	sort.SliceStable(out.Results, func(a, b int) bool { return out.Results[a].ModifiedAt > out.Results[b].ModifiedAt })
	out.Truncated = out.Truncated || float64(len(out.Results)) > limit
	out.Results = out.Results[:sliceEnd(len(out.Results), limit)]
	return out, nil
}
func (s *Service) verifyIndexed(ctx context.Context, indexed *localindex.ContentQueryResult, root, query string, sensitive bool) bool {
	sources := map[string]string{}
	entries := map[string]protocol.JSONObject{}
	needle := query
	if !sensitive {
		needle = localindex.NormalizeContent(needle)
	}
	inside := func(p string) bool {
		absolute, e := filepath.Abs(p)
		if e != nil {
			return false
		}
		relative, e := filepath.Rel(root, absolute)
		return e == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
	}
	for _, session := range indexed.Sessions {
		if ctx.Err() != nil || !inside(session.OwnerTranscriptPath) {
			return false
		}
		owner, e := os.Stat(session.OwnerTranscriptPath)
		if e != nil || !owner.Mode().IsRegular() {
			return false
		}
		for _, match := range session.Matches {
			if !inside(match.SourcePath) || ctx.Err() != nil {
				return false
			}
			fingerprint := localindex.DeserializeFingerprint(match.SourceFingerprint)
			if fingerprint == nil || !matchFitsFingerprint(match, *fingerprint) {
				return false
			}
			if old, ok := sources[match.SourcePath]; ok {
				if old != match.SourceFingerprint {
					return false
				}
			} else {
				if localindex.VerifyFingerprint(ctx, match.SourcePath, *fingerprint).Kind != "unchanged" {
					return false
				}
				sources[match.SourcePath] = match.SourceFingerprint
			}
			key := match.SourcePath + "\x00" + strconvI(match.ByteStart) + "\x00" + strconvI(match.ByteLength)
			entry, ok := entries[key]
			if !ok {
				entry, e = readCanonicalEntry(ctx, match.SourcePath, match.ByteStart, match.ByteLength, *fingerprint)
				if e != nil {
					return false
				}
				entries[key] = entry
			}
			segments := localindex.ExtractSearchableSegments(entry)
			if match.SegmentIndex < 0 || match.SegmentIndex >= int64(len(segments)) {
				return false
			}
			segment := segments[match.SegmentIndex]
			body := segment.Text
			if !sensitive {
				body = localindex.NormalizeContent(body)
			}
			var id, stamp *string
			if isString(entry.Get("uuid")) {
				id = ptr(str(entry.Get("uuid")))
			}
			if isString(entry.Get("timestamp")) {
				stamp = ptr(str(entry.Get("timestamp")))
			}
			if segment.Role != match.Role || segment.Text != match.Body || !sameOptionalString(id, match.MessageID) || !sameOptionalString(stamp, match.Timestamp) || !strings.Contains(body, needle) {
				return false
			}
		}
	}
	return true
}
