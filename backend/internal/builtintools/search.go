package builtintools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) relative(p string) string {
	if under(s.options.CWD, p) {
		r, e := filepath.Rel(s.options.CWD, p)
		if e == nil {
			return r
		}
	}
	return p
}
func (s *Service) startSearch(ctx context.Context, args []string, p string, scope *cooperative.Scope) (*ownerasync.OwnerFuture[[]string], error) {
	f, e := native(s, scope, func() (processResult, error) {
		command, prefix := "rg", []string{}
		if config := s.options.RipgrepConfig; config != nil {
			var e error
			command, prefix, e = config()
			if e != nil {
				return processResult{}, e
			}
		}
		return s.process(ctx, command, append(append(prefix, args...), "--", p), s.options.CWD, 20*time.Second)
	})
	if e != nil {
		return nil, e
	}
	return transform(s, scope, f, func(_ *cooperative.Scope, result processResult) ([]string, error) {
		if result.Interrupted {
			return nil, fmt.Errorf("Ripgrep search timed out or was interrupted; the search did not complete")
		}
		if result.Code > 1 {
			return nil, fmt.Errorf("ripgrep error: %s", strings.TrimSpace(result.Err))
		}
		text := strings.TrimRight(result.Out, "\r\n")
		if text == "" {
			return []string{}, nil
		}
		return strings.Split(text, "\n"), nil
	})
}
func truth(v string, d bool) bool {
	if v == "" {
		return d
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func (s *Service) appendReadExclusions(scope *cooperative.Scope, args []string, p string) ([]string, error) {
	c, e := s.options.PermissionContext(scope)
	if e != nil {
		return nil, e
	}
	for source, list := range rules(c, "alwaysDenyRules") {
		for _, rule := range list {
			if !strings.HasPrefix(rule, "Read(") {
				continue
			}
			pattern := strings.TrimSuffix(strings.TrimPrefix(rule, "Read("), ")")
			if strings.HasPrefix(pattern, "/") && !strings.HasPrefix(pattern, "//") {
				root := s.options.PermissionRoots[source]
				if root == "" {
					return nil, capability("search ignore-rule source " + source)
				}
				pattern = filepath.Join(root, pattern[1:])
			}
			if strings.HasPrefix(pattern, "~/") {
				pattern = filepath.Join(s.options.Home, pattern[2:])
			}
			if strings.HasPrefix(pattern, "//") {
				pattern = pattern[1:]
			}
			if filepath.IsAbs(pattern) {
				if rel, e := filepath.Rel(p, pattern); e == nil {
					pattern = rel
				}
			}
			args = append(args, "--glob", "!"+filepath.ToSlash(pattern))
		}
	}
	return args, nil
}
func (s *Service) glob(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope) (json.RawMessage, error) {
	future, e := s.startGlob(ctx, in, scope)
	if e != nil {
		return nil, e
	}
	return future.Await(scope)
}
func (s *Service) startGlob(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope) (*ownerasync.OwnerFuture[json.RawMessage], error) {
	p, e := s.path(in, "path")
	if e != nil {
		return nil, e
	}
	pattern, e := text(in.Get("pattern"))
	if e != nil {
		return nil, e
	}
	if filepath.IsAbs(pattern) {
		idx := strings.IndexAny(pattern, "*?[{")
		if idx < 0 {
			p, pattern = filepath.Dir(pattern), filepath.Base(pattern)
		} else {
			separator := strings.LastIndex(pattern[:idx], "/")
			if separator >= 0 {
				p, pattern = pattern[:separator], pattern[separator+1:]
				if p == "" {
					p = "/"
				}
			}
		}
	}
	args := []string{"--files", "--glob", pattern, "--sort=modified"}
	if truth(s.options.Environment["CLAUDE_CODE_GLOB_NO_IGNORE"], true) {
		args = append(args, "--no-ignore")
	}
	if truth(s.options.Environment["CLAUDE_CODE_GLOB_HIDDEN"], true) {
		args = append(args, "--hidden")
	}
	args, e = s.appendReadExclusions(scope, args, p)
	if e != nil {
		return nil, e
	}
	start := s.options.NowMillis()
	future, e := s.startSearch(ctx, args, p, scope)
	if e != nil {
		return nil, e
	}
	return transform(s, scope, future, func(_ *cooperative.Scope, names []string) (json.RawMessage, error) {
		truncated := len(names) > 100
		if truncated {
			names = names[:100]
		}
		for i, n := range names {
			if !filepath.IsAbs(n) {
				n = filepath.Join(p, n)
			}
			names[i] = s.relative(n)
		}
		return object(field("filenames", names), field("durationMs", s.options.NowMillis()-start), field("numFiles", len(names)), field("truncated", truncated)), nil
	})
}
func (s *Service) grep(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope) (json.RawMessage, error) {
	future, e := s.startGrep(ctx, in, scope)
	if e != nil {
		return nil, e
	}
	return future.Await(scope)
}
func (s *Service) startGrep(ctx context.Context, in protocol.JSONObject, scope *cooperative.Scope) (*ownerasync.OwnerFuture[json.RawMessage], error) {
	p, e := s.path(in, "path")
	if e != nil {
		return nil, e
	}
	pattern, e := text(in.Get("pattern"))
	if e != nil {
		return nil, e
	}
	mode := in.String("output_mode")
	if mode == "" {
		mode = "files_with_matches"
	}
	args := []string{"--hidden"}
	for _, dir := range []string{".git", ".svn", ".hg", ".bzr", ".jj", ".sl"} {
		args = append(args, "--glob", "!"+dir)
	}
	args = append(args, "--max-columns", "500")
	if boolean(in, "multiline", false) {
		args = append(args, "-U", "--multiline-dotall")
	}
	if boolean(in, "-i", false) {
		args = append(args, "-i")
	}
	switch mode {
	case "files_with_matches":
		args = append(args, "-l")
	case "count":
		args = append(args, "-c")
	case "content":
		if boolean(in, "-n", true) {
			args = append(args, "-n")
		}
		if in.Get("context") != nil {
			args = append(args, "-C", strconv.Itoa(number(in, "context", 0)))
		} else if in.Get("-C") != nil {
			args = append(args, "-C", strconv.Itoa(number(in, "-C", 0)))
		} else {
			for _, k := range []string{"-B", "-A"} {
				if in.Get(k) != nil {
					args = append(args, k, strconv.Itoa(number(in, k, 0)))
				}
			}
		}
	}
	args = append(args, "-e", pattern)
	if typ := in.String("type"); typ != "" {
		args = append(args, "--type", typ)
	}
	if glob := in.String("glob"); glob != "" {
		for _, p := range strings.Fields(glob) {
			patterns := []string{p}
			if !strings.Contains(p, "{") || !strings.Contains(p, "}") {
				patterns = strings.Split(p, ",")
			}
			for _, g := range patterns {
				if g != "" {
					args = append(args, "--glob", g)
				}
			}
		}
	}
	args, e = s.appendReadExclusions(scope, args, p)
	if e != nil {
		return nil, e
	}
	future, e := s.startSearch(ctx, args, p, scope)
	if e != nil {
		return nil, e
	}
	return transform(s, scope, future, func(_ *cooperative.Scope, lines []string) (json.RawMessage, error) {
		limit, offset := number(in, "head_limit", 250), number(in, "offset", 0)
		if offset < 0 {
			offset = len(lines) + offset
			if offset < 0 {
				offset = 0
			}
		}
		if offset > len(lines) {
			offset = len(lines)
		}
		lines = lines[offset:]
		truncated := limit != 0 && len(lines) > limit
		if limit > 0 && len(lines) > limit {
			lines = lines[:limit]
		}
		out := protocol.NewJSONObject(field("mode", mode), field("numFiles", 0), field("filenames", []string{}))
		if truncated {
			out.Set("appliedLimit", raw(limit))
		}
		if number(in, "offset", 0) > 0 {
			out.Set("appliedOffset", in.Get("offset"))
		}
		switch mode {
		case "content":
			for i, l := range lines {
				if colon := strings.IndexByte(l, ':'); colon > 0 {
					lines[i] = s.relative(l[:colon]) + l[colon:]
				}
			}
			out.Set("content", quote(strings.Join(lines, "\n")))
			out.Set("numLines", raw(len(lines)))
		case "count":
			matches, files := 0, 0
			for i, l := range lines {
				if colon := strings.LastIndexByte(l, ':'); colon > 0 {
					lines[i] = s.relative(l[:colon]) + l[colon:]
					if n, e := strconv.Atoi(l[colon+1:]); e == nil {
						matches += n
						files++
					}
				}
			}
			out.Set("content", quote(strings.Join(lines, "\n")))
			out.Set("numMatches", raw(matches))
			out.Set("numFiles", raw(files))
		default:
			for i, l := range lines {
				lines[i] = s.relative(l)
			}
			out.Set("numFiles", raw(len(lines)))
			out.Set("filenames", raw(lines))
		}
		return raw(out), nil
	})
}
func mapGrep(in protocol.JSONObject, id json.RawMessage) protocol.JSONObject {
	parts := []string{}
	if in.Get("appliedLimit") != nil {
		parts = append(parts, "limit: "+string(in.Get("appliedLimit")))
	}
	if number(in, "appliedOffset", 0) != 0 {
		parts = append(parts, "offset: "+string(in.Get("appliedOffset")))
	}
	pagination := strings.Join(parts, ", ")
	mode := in.String("mode")
	content := in.String("content")
	if mode == "content" {
		if content == "" {
			content = "No matches found"
		}
		if pagination != "" {
			content += "\n\n[Showing results with pagination = " + pagination + "]"
		}
		return textResult(id, content)
	}
	if mode == "count" {
		if content == "" {
			content = "No matches found"
		}
		matches, files := number(in, "numMatches", 0), number(in, "numFiles", 0)
		occurrence, file := "occurrences", "files"
		if matches == 1 {
			occurrence = "occurrence"
		}
		if files == 1 {
			file = "file"
		}
		content += fmt.Sprintf("\n\nFound %d total %s across %d %s.", matches, occurrence, files, file)
		if pagination != "" {
			content += " with pagination = " + pagination
		}
		return textResult(id, content)
	}
	n := number(in, "numFiles", 0)
	if n == 0 {
		return textResult(id, "No files found")
	}
	var names []string
	_ = json.Unmarshal(in.Get("filenames"), &names)
	word := "files"
	if n == 1 {
		word = "file"
	}
	prefix := fmt.Sprintf("Found %d %s", n, word)
	if pagination != "" {
		prefix += " " + pagination
	}
	return textResult(id, prefix+"\n"+strings.Join(names, "\n"))
}
