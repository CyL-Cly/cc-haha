// Package terminaldiff implements the frozen DiffDialog's data producers.
package terminaldiff

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sourcecollation"
)

type Hunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}
type File struct {
	Path         string `json:"path"`
	LinesAdded   int    `json:"linesAdded"`
	LinesRemoved int    `json:"linesRemoved"`
	IsBinary     bool   `json:"isBinary"`
	IsLargeFile  bool   `json:"isLargeFile"`
	IsTruncated  bool   `json:"isTruncated"`
	IsNewFile    bool   `json:"isNewFile,omitempty"`
	IsUntracked  bool   `json:"isUntracked,omitempty"`
	Hunks        []Hunk `json:"hunks"`
}
type Stats struct {
	FilesCount   int `json:"filesCount"`
	LinesAdded   int `json:"linesAdded"`
	LinesRemoved int `json:"linesRemoved"`
}
type Data struct {
	Stats   *Stats `json:"stats"`
	Files   []File `json:"files"`
	Loading bool   `json:"loading"`
}
type Source struct {
	TurnIndex         int    `json:"turnIndex"`
	UserPromptPreview string `json:"userPromptPreview"`
	Data              Data   `json:"data"`
	promptJSON        json.RawMessage
}

func (s Source) MarshalJSON() ([]byte, error) {
	preview := s.promptJSON
	if preview == nil {
		preview, _ = json.Marshal(s.UserPromptPreview)
	}
	body, _ := json.Marshal(s.Data)
	return protocol.NewJSONObject(protocol.JSONField{Name: "turnIndex", Value: json.RawMessage(strconv.Itoa(s.TurnIndex))}, protocol.JSONField{Name: "userPromptPreview", Value: preview}, protocol.JSONField{Name: "data", Value: body}).MarshalJSON()
}
func (s *Source) UnmarshalJSON(data []byte) error {
	var row struct {
		TurnIndex int             `json:"turnIndex"`
		Preview   json.RawMessage `json:"userPromptPreview"`
		Data      Data            `json:"data"`
	}
	if e := json.Unmarshal(data, &row); e != nil {
		return e
	}
	s.TurnIndex, s.Data = row.TurnIndex, row.Data
	s.promptJSON = append(json.RawMessage(nil), row.Preview...)
	return json.Unmarshal(row.Preview, &s.UserPromptPreview)
}

type Dialog struct {
	Sources []Source `json:"sources"`
}

// CommandOutput matches execa's default stripFinalNewline at the actual
// execFileNoThrow boundary. parseGitDiff itself still accepts trailing empty
// context lines when its caller supplies them explicitly.
func CommandOutput(value string) string {
	if strings.HasSuffix(value, "\n") {
		value = strings.TrimSuffix(value, "\n")
		value = strings.TrimSuffix(value, "\r")
	}
	return value
}

var shortstatPattern = regexp.MustCompile(`(\d+)\s+files?\s+changed(?:,\s+(\d+)\s+insertions?\(\+\))?(?:,\s+(\d+)\s+deletions?\(-\))?`)
var hunkPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
var filePattern = regexp.MustCompile(`^a/(.+?) b/(.+)$`)

func number(s string) int { n, _ := strconv.Atoi(s); return n }
func Shortstat(s string) *Stats {
	m := shortstatPattern.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	return &Stats{number(m[1]), number(m[2]), number(m[3])}
}

// ParseHunks retains Source's separate 50-file, one-million UTF-16-unit and
// 400-line limits. Empty context lines count toward the per-file budget.
func ParseHunks(output string) map[string][]Hunk {
	out := map[string][]Hunk{}
	for _, part := range regexp.MustCompile(`(?m)^diff --git `).Split(output, -1) {
		if len(out) >= 50 {
			break
		}
		if len(utf16.Encode([]rune(part))) > 1_000_000 {
			continue
		}
		lines := strings.Split(part, "\n")
		m := filePattern.FindStringSubmatch(lines[0])
		if m == nil {
			continue
		}
		hunks := []Hunk{}
		var current *Hunk
		count := 0
		for _, line := range lines[1:] {
			if h := hunkPattern.FindStringSubmatch(line); h != nil {
				if current != nil {
					hunks = append(hunks, *current)
				}
				oldN, newN := 1, 1
				if h[2] != "" {
					oldN = number(h[2])
				}
				if h[4] != "" {
					newN = number(h[4])
				}
				current = &Hunk{number(h[1]), oldN, number(h[3]), newN, []string{}}
				continue
			}
			skip := false
			for _, prefix := range []string{"index ", "---", "+++", "new file", "deleted file", "old mode", "new mode", "Binary files"} {
				if strings.HasPrefix(line, prefix) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			if current != nil && (line == "" || strings.ContainsRune("+- ", rune(line[0]))) && count < 400 {
				current.Lines = append(current.Lines, line)
				count++
			}
		}
		if current != nil {
			hunks = append(hunks, *current)
		}
		if len(hunks) > 0 {
			out[m[2]] = hunks
		}
	}
	return out
}
func SortFiles(files []File) error {
	c, e := sourcecollation.New()
	if e != nil {
		return e
	}
	defer c.Close()
	sort.SliceStable(files, func(i, j int) bool {
		return c.Compare(utf16.Encode([]rune(files[i].Path)), utf16.Encode([]rune(files[j].Path))) < 0
	})
	return nil
}
func Current(shortstat, numstat, untracked, diff string) (Data, error) {
	data := Data{Stats: &Stats{}, Files: []File{}}
	if quick := Shortstat(shortstat); quick != nil && quick.FilesCount > 500 {
		data.Stats = quick
		return data, nil
	}
	index := map[string]int{}
	hunks := ParseHunks(diff)
	for _, line := range strings.Split(strings.TrimSpace(numstat), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		data.Stats.FilesCount++
		binary := parts[0] == "-" || parts[1] == "-"
		added, removed := number(parts[0]), number(parts[1])
		if binary {
			added, removed = 0, 0
		}
		data.Stats.LinesAdded += added
		data.Stats.LinesRemoved += removed
		path := strings.Join(parts[2:], "\t")
		if len(index) >= 50 {
			continue
		}
		f := File{Path: path, LinesAdded: added, LinesRemoved: removed, IsBinary: binary, Hunks: hunks[path]}
		f.IsLargeFile = !binary && f.Hunks == nil
		f.IsTruncated = !f.IsLargeFile && !binary && added+removed > 400
		if at, ok := index[path]; ok {
			data.Files[at] = f
		} else {
			index[path] = len(data.Files)
			data.Files = append(data.Files, f)
		}
	}
	remaining := 50 - len(index)
	for _, path := range strings.Split(strings.TrimSpace(untracked), "\n") {
		if remaining <= 0 {
			break
		}
		if path == "" {
			continue
		}
		remaining--
		data.Stats.FilesCount++
		f := File{Path: path, IsUntracked: true, Hunks: hunks[path]}
		if at, ok := index[path]; ok {
			data.Files[at] = f
		} else {
			index[path] = len(data.Files)
			data.Files = append(data.Files, f)
		}
	}
	return data, SortFiles(data.Files)
}

// Turns follows useTurnDiffs' original user/toolResult/isMeta partition. The
// input is the current REPL history, including structured toolUseResult data.
func Turns(messages []protocol.JSONObject) ([]Source, error) {
	out := []Source{}
	var current *Source
	turn := 0
	finish := func() {
		if current != nil && len(current.Data.Files) > 0 {
			out = append(out, *current)
		}
	}
	for _, msg := range messages {
		if msg.String("type") != "user" {
			continue
		}
		body, _ := msg.Object("message")
		var blocks []protocol.JSONObject
		_ = json.Unmarshal(body.Get("content"), &blocks)
		toolResult := msg.Get("toolUseResult")
		isResult := openaiauth.AuthTruthy(toolResult) || (len(blocks) > 0 && blocks[0].String("type") == "tool_result")
		if !isResult && !openaiauth.AuthTruthy(msg.Get("isMeta")) {
			finish()
			turn++
			preview := ""
			var previewJSON json.RawMessage
			if raw := body.Get("content"); len(raw) > 0 && raw[0] == '"' {
				units, e := protocol.DecodeJavaScriptJSONString(raw)
				if e != nil {
					return nil, e
				}
				if len(units) > 30 {
					units = append(units[:29:29], 0x2026)
				}
				preview = string(utf16.Decode(units))
				previewJSON = protocol.QuoteJavaScriptUTF16(units)
			}
			current = &Source{TurnIndex: turn, UserPromptPreview: preview, promptJSON: previewJSON, Data: Data{Stats: &Stats{}, Files: []File{}}}
			continue
		}
		if current == nil || !openaiauth.AuthTruthy(toolResult) {
			continue
		}
		var result protocol.JSONObject
		if json.Unmarshal(toolResult, &result) != nil {
			continue
		}
		path := result.String("filePath")
		var patch []Hunk
		_ = json.Unmarshal(result.Get("structuredPatch"), &patch)
		content := result.Get("content")
		create := result.String("type") == "create" && len(content) > 0 && content[0] == '"'
		if path == "" || len(patch) == 0 && !create {
			continue
		}
		at := -1
		for i, f := range current.Data.Files {
			if f.Path == path {
				at = i
				break
			}
		}
		if at < 0 {
			at = len(current.Data.Files)
			current.Data.Files = append(current.Data.Files, File{Path: path, Hunks: []Hunk{}})
		}
		f := &current.Data.Files[at]
		if create && len(patch) == 0 {
			var s string
			_ = json.Unmarshal(content, &s)
			lines := strings.Split(s, "\n")
			for i := range lines {
				lines[i] = "+" + lines[i]
			}
			patch = []Hunk{{0, 0, 1, len(lines), lines}}
		}
		f.Hunks = append(f.Hunks, patch...)
		f.IsNewFile = f.IsNewFile || create
		for _, h := range patch {
			for _, line := range h.Lines {
				if strings.HasPrefix(line, "+") {
					f.LinesAdded++
					current.Data.Stats.LinesAdded++
				} else if strings.HasPrefix(line, "-") {
					f.LinesRemoved++
					current.Data.Stats.LinesRemoved++
				}
			}
		}
		current.Data.Stats.FilesCount = len(current.Data.Files)
	}
	finish()
	for i := range out {
		if e := SortFiles(out[i].Data.Files); e != nil {
			return nil, e
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
