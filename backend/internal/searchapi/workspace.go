package searchapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) processEnv() []string {
	keys := []string{}
	for k := range s.o.Environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []string{}
	for _, k := range keys {
		out = append(out, k+"="+s.o.Environment[k])
	}
	return out
}
func (s *Service) commandPath(name string) string {
	for _, dir := range filepath.SplitList(s.o.Environment["PATH"]) {
		if dir == "" {
			continue
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(s.o.WorkDir, dir)
		}
		for _, suffix := range []string{"", ".exe", ".cmd", ".bat"} {
			p := filepath.Join(dir, name+suffix)
			if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
				return p
			}
		}
	}
	return ""
}
func (s *Service) run(ctx context.Context, c Command, args []string) ([]byte, error) {
	if c.Path == "" {
		return nil, fmt.Errorf("Search command is unavailable")
	}
	cmd := exec.CommandContext(ctx, c.Path, append(append([]string{}, c.Args...), args...)...)
	cmd.Dir = s.o.WorkDir
	cmd.Env = s.processEnv()
	if c.Argv0 != "" {
		cmd.Args[0] = c.Argv0
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if !errorsAsExit(err, &exit) || exit.ExitCode() != 1 {
			return nil, fmt.Errorf("Search command failed: %w", err)
		}
	} else if len(data) == 0 {
		return nil, fmt.Errorf("Search command returned no searchable output")
	}
	return data, nil
}
func errorsAsExit(err error, dest **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*dest = e
	}
	return ok
}
func (s *Service) SearchWorkspace(ctx context.Context, query string, o protocol.JSONObject) ([]WorkspaceResult, error) {
	if query == "" {
		return nil, bad("Search query is required")
	}
	cwd := s.o.WorkDir
	if truthy(o.Get("cwd")) {
		cwd = str(o.Get("cwd"))
	}
	maximum := 200.0
	maximumArgument := "200"
	if truthy(o.Get("maxResults")) {
		maximum = number(o.Get("maxResults"), 200)
		if converted, e := protocol.JavaScriptStringJSON(o.Get("maxResults")); e == nil {
			maximumArgument = str(converted)
		}
	}
	sensitive := string(o.Get("caseSensitive")) != "false"
	glob := str(o.Get("glob"))
	if rg := s.o.Ripgrep(); rg.Path != "" {
		args := []string{"--json", "--max-count", maximumArgument}
		if !sensitive {
			args = append(args, "--ignore-case")
		}
		args = append(args, "-C", "4")
		if glob != "" {
			args = append(args, "--glob", glob)
		}
		args = append(args, "--", query, cwd)
		data, e := s.run(ctx, rg, args)
		if e == nil {
			return parseRGWorkspace(data, maximum), nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if grep := s.commandPath("grep"); grep != "" {
		args := []string{"-rn", "--max-count", maximumArgument}
		if !sensitive {
			args = append(args, "-i")
		}
		if glob != "" {
			args = append(args, "--include", glob)
		}
		args = append(args, "--", query, cwd)
		data, e := s.run(ctx, Command{Path: grep}, args)
		if e == nil {
			return parseGrep(data, maximum), nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	results := []WorkspaceResult{}
	needle := query
	if !sensitive {
		needle = localindex.NormalizeContent(needle)
	}
	var walk func(string) error
	walk = func(dir string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if float64(len(results)) >= maximum {
			return nil
		}
		entries, e := readDirectory(dir)
		if e != nil {
			return nil
		}
		for _, entry := range entries {
			if float64(len(results)) >= maximum {
				return nil
			}
			if entry.Name() == "node_modules" || entry.Name() == ".git" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				if e := walk(path); e != nil {
					return e
				}
				continue
			}
			if !entry.Type().IsRegular() || glob != "" && !simpleGlob(entry.Name(), glob) {
				continue
			}
			data, e := os.ReadFile(path)
			if e != nil || bytes.IndexByte(data, 0) >= 0 {
				continue
			}
			for n, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
				if float64(len(results)) >= maximum {
					break
				}
				haystack := line
				if !sensitive {
					haystack = localindex.NormalizeContent(line)
				}
				if strings.Contains(haystack, needle) {
					results = append(results, WorkspaceResult{File: path, Line: int64(n + 1), Text: line})
				}
			}
		}
		return nil
	}
	e := walk(cwd)
	return results, e
}
func readDirectory(path string) ([]os.DirEntry, error) {
	directory, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer directory.Close()
	return directory.ReadDir(-1)
}
func simpleGlob(name, pattern string) bool {
	if !strings.Contains(pattern, "*") {
		return name == pattern
	}
	rx := regexp.MustCompile("^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$")
	return rx.MatchString(name)
}
func parseRGWorkspace(data []byte, maximum float64) []WorkspaceResult {
	results := []WorkspaceResult{}
	positions := map[string]int{}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var row struct {
			Type string
			Data struct {
				Path       struct{ Text string }
				LineNumber int64 `json:"line_number"`
				Lines      struct{ Text string }
			}
		}
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		file := row.Data.Path.Text
		text := strings.TrimSuffix(row.Data.Lines.Text, "\n")
		if row.Type == "match" {
			key := file + ":" + strconv.FormatInt(row.Data.LineNumber, 10)
			value := WorkspaceResult{File: file, Line: row.Data.LineNumber, Text: text}
			if n, ok := positions[key]; ok {
				results[n] = value
			} else {
				positions[key] = len(results)
				results = append(results, value)
			}
		} else if row.Type == "context" {
			for n := range results {
				if strings.HasPrefix(results[n].File+":"+strconv.FormatInt(results[n].Line, 10), file+":") {
					results[n].Context = append(results[n].Context, text)
				}
			}
		}
	}
	if maximum <= 0 {
		return []WorkspaceResult{}
	}
	if mathNaN(maximum) {
		return results
	}
	count := int(math.Ceil(maximum))
	return results[:min(len(results), count)]
}
func mathNaN(n float64) bool { return n != n }

var grepLine = regexp.MustCompile(`^(.+?):(\d+):(.*)$`)

func parseGrep(data []byte, maximum float64) []WorkspaceResult {
	results := []WorkspaceResult{}
	for _, line := range strings.Split(string(data), "\n") {
		if float64(len(results)) >= maximum {
			break
		}
		m := grepLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.ParseInt(m[2], 10, 64)
		results = append(results, WorkspaceResult{File: m[1], Line: n, Text: m[3]})
	}
	return results
}
