// Package doctorapi implements the original read-only state inspection. Repair
// remains a protected dry run; it cannot mutate user configuration or sessions.
package doctorapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type Options struct {
	HomeDir, ConfigDir, ProjectRoot string
	UsesConfigDirOverride           bool
	AgentSkillsEnabled              func(context.Context) bool
	Diagnostics                     *diagnosticsapi.Service
	Now                             func() time.Time
}
type Service struct{ options Options }
type Item struct {
	ID               string `json:"id"`
	Label            string `json:"label"`
	Kind             string `json:"kind"`
	Scope            string `json:"scope"`
	Path             string `json:"path"`
	Protected        bool   `json:"protected"`
	Exists           bool   `json:"exists"`
	Status           string `json:"status"`
	Bytes            int    `json:"bytes"`
	EntryCount       *int   `json:"entryCount,omitempty"`
	LineCount        *int   `json:"lineCount,omitempty"`
	InvalidLineCount *int   `json:"invalidLineCount,omitempty"`
	Error            string `json:"error,omitempty"`
}
type Skip struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}
type Report struct {
	GeneratedAt    string        `json:"generatedAt"`
	Items          []Item        `json:"items"`
	ProtectedSkips []Skip        `json:"protectedSkips"`
	Summary        ReportSummary `json:"summary"`
}
type ReportSummary struct {
	Total          int `json:"total"`
	ProtectedCount int `json:"protectedCount"`
	NeutralCount   int `json:"neutralCount"`
	MissingCount   int `json:"missingCount"`
	InvalidCount   int `json:"invalidCount"`
}
type Operation struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Action string `json:"action"`
}
type Repair struct {
	DryRun     bool          `json:"dryRun"`
	Mutated    bool          `json:"mutated"`
	Operations []Operation   `json:"operations"`
	Skips      []Skip        `json:"skips"`
	Summary    RepairSummary `json:"summary"`
}
type RepairSummary struct {
	OperationCount int `json:"operationCount"`
	SkipCount      int `json:"skipCount"`
}
type target struct{ id, label, kind, scope, path string }

func New(options Options) (*Service, error) {
	if !filepath.IsAbs(options.ConfigDir) || !filepath.IsAbs(options.HomeDir) {
		return nil, errors.New("Doctor requires explicit absolute home/config paths")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.AgentSkillsEnabled == nil {
		options.AgentSkillsEnabled = func(context.Context) bool { return true }
	}
	if options.Diagnostics == nil {
		var err error
		options.Diagnostics, err = diagnosticsapi.New(diagnosticsapi.Options{HomeDir: options.HomeDir, ConfigDir: options.ConfigDir})
		if err != nil {
			return nil, err
		}
	}
	return &Service{options}, nil
}
func (s *Service) ForProject(root string) *Service {
	options := s.options
	options.ProjectRoot = root
	return &Service{options}
}
func (s *Service) GetReport(ctx context.Context) (Report, error) {
	result := Report{GeneratedAt: s.options.Now().UTC().Format("2006-01-02T15:04:05.000Z"), Items: []Item{}, ProtectedSkips: []Skip{}}
	for _, target := range s.targets(ctx) {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		item := s.inspect(target)
		result.Items = append(result.Items, item)
		result.ProtectedSkips = append(result.ProtectedSkips, Skip{item.ID, item.Path, "protected"})
		switch item.Status {
		case "not_configured":
			result.Summary.NeutralCount++
		case "missing":
			result.Summary.MissingCount++
		case "invalid_json", "invalid_jsonl", "invalid_schema", "unreadable":
			result.Summary.InvalidCount++
		}
	}
	result.Summary.Total = len(result.Items)
	result.Summary.ProtectedCount = len(result.ProtectedSkips)
	return result, nil
}
func (s *Service) Repair(ctx context.Context, ids []string) (Repair, error) {
	report, err := s.GetReport(ctx)
	if err != nil {
		return Repair{}, err
	}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	result := Repair{DryRun: true, Mutated: false, Operations: []Operation{}, Skips: []Skip{}}
	for _, item := range report.Items {
		if len(selected) > 0 && !selected[item.ID] {
			continue
		}
		result.Skips = append(result.Skips, Skip{item.ID, item.Path, "protected"})
	}
	result.Summary.SkipCount = len(result.Skips)
	return result, nil
}
func (s *Service) targets(ctx context.Context) []target {
	config, home := s.options.ConfigDir, s.options.HomeDir
	enabled := s.options.AgentSkillsEnabled(ctx)
	userMCP := filepath.Join(home, ".claude.json")
	if s.options.UsesConfigDirOverride {
		userMCP = filepath.Join(config, ".claude.json")
	}
	rows := []target{{"user-settings", "User settings", "json", "user", filepath.Join(config, "settings.json")}, {"cc-haha-providers", "Managed providers", "json", "user", filepath.Join(config, "cc-haha", "providers.json")}, {"cc-haha-settings", "Managed provider settings", "json", "user", filepath.Join(config, "cc-haha", "settings.json")}, {"adapters", "Adapters config", "json", "user", filepath.Join(config, "adapters.json")}, {"adapter-sessions", "Adapter sessions", "json", "user", filepath.Join(config, "adapter-sessions.json")}, {"user-skills", "User skills", "directory", "user", filepath.Join(config, "skills")}}
	if enabled {
		rows = append(rows, target{"user-agent-skills", "User skills (.agents)", "directory", "user", filepath.Join(home, ".agents", "skills")})
	}
	rows = append(rows, target{"teams", "Teams", "directory", "user", filepath.Join(config, "teams")}, target{"plugins", "Plugins", "directory", "user", filepath.Join(config, "plugins")}, target{"cowork-plugins", "Cowork plugins", "directory", "user", filepath.Join(config, "cowork_plugins")}, target{"user-mcp", "User MCP config", "json", "user", userMCP}, target{"oauth", "OAuth tokens", "json", "user", filepath.Join(config, "cc-haha", "oauth.json")}, target{"openai-oauth", "OpenAI OAuth tokens", "json", "user", filepath.Join(config, "cc-haha", "openai-oauth.json")}, target{"grok-oauth", "Grok OAuth tokens", "json", "user", filepath.Join(config, "cc-haha", "grok-oauth.json")})
	if root := s.options.ProjectRoot; root != "" {
		rows = append(rows, target{"project-settings", "Project settings", "json", "project", filepath.Join(root, ".claude", "settings.json")}, target{"project-skills", "Project skills", "directory", "project", filepath.Join(root, ".claude", "skills")})
		if enabled {
			rows = append(rows, target{"project-agent-skills", "Project skills (.agents)", "directory", "project", filepath.Join(root, ".agents", "skills")})
		}
		rows = append(rows, target{"project-mcp", "Project MCP config", "json", "project", filepath.Join(root, ".mcp.json")})
	}
	for _, path := range s.jsonlFiles(filepath.Join(config, "projects")) {
		relative, _ := filepath.Rel(config, path)
		rows = append(rows, target{"session-jsonl:" + filepath.ToSlash(relative), "Session transcript", "jsonl", "user", path})
	}
	return rows
}
func (s *Service) inspect(t target) Item {
	item := Item{ID: t.id, Label: t.label, Kind: t.kind, Scope: t.scope, Path: s.alias(t.path), Protected: true, Status: "not_configured"}
	if _, err := os.Stat(t.path); err != nil {
		return item
	}
	item.Exists = true
	if t.kind == "directory" {
		entries, err := os.ReadDir(t.path)
		if err != nil {
			item.Status = "unreadable"
			item.Error = s.sanitize(err.Error())
			return item
		}
		count := 0
		for _, entry := range entries {
			if entry.Name() != ".DS_Store" {
				count++
			}
		}
		item.Status = "ok"
		item.EntryCount = &count
		return item
	}
	raw, err := os.ReadFile(t.path)
	if err != nil {
		item.Status = "unreadable"
		item.Error = s.sanitize(err.Error())
		return item
	}
	raw = protocol.DecodeJavaScriptUTF8(raw)
	item.Bytes = len(raw)
	if t.kind == "jsonl" {
		count, invalid := 0, 0
		for _, line := range strings.Split(string(raw), "\n") {
			if trim(line) == "" {
				continue
			}
			count++
			if !json.Valid([]byte(line)) {
				invalid++
			}
		}
		item.LineCount = &count
		item.InvalidLineCount = &invalid
		item.Status = "ok"
		if invalid > 0 {
			item.Status = "invalid_jsonl"
		}
		return item
	}
	if trim(string(raw)) == "" {
		item.Status = "invalid_json"
		item.Error = "Empty JSON file"
		return item
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	err = decoder.Decode(&value)
	if err == nil && !json.Valid(raw) {
		var parsed json.RawMessage
		err = json.Unmarshal(raw, &parsed)
	}
	if err != nil {
		item.Status = "invalid_json"
		item.Error = s.sanitize(parseError(raw, err))
		return item
	}
	if t.id == "cc-haha-providers" {
		if failure := validateProviders(raw); failure != "" {
			item.Status = "invalid_schema"
			item.Error = s.sanitize(failure)
			return item
		}
	}
	count := 1
	switch v := value.(type) {
	case nil:
		count = 0
	case []any:
		count = len(v)
	case map[string]any:
		count = len(v)
	}
	item.Status = "ok"
	item.EntryCount = &count
	return item
}

func trim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r >= '\t' && r <= '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}
func (s *Service) jsonlFiles(root string) []string {
	result := []string{}
	stack := []string{root}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(current)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(current, entry.Name())
			if entry.IsDir() {
				stack = append(stack, path)
			} else if entry.Type().IsRegular() && strings.HasSuffix(path, ".jsonl") {
				result = append(result, path)
			}
		}
	}
	collator := collate.New(language.English)
	collator.SortStrings(result)
	return result
}
func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || !strings.HasPrefix(relative, "..") && !filepath.IsAbs(relative))
}
func (s *Service) alias(path string) string {
	for _, row := range []struct{ root, label string }{{s.options.ProjectRoot, "<project>"}, {s.options.ConfigDir, "~/.claude"}, {s.options.HomeDir, "~"}} {
		if row.root != "" && within(row.root, path) {
			relative, _ := filepath.Rel(row.root, path)
			if relative == "." {
				return row.label
			}
			return row.label + "/" + filepath.ToSlash(relative)
		}
	}
	return s.sanitize(path)
}
func (s *Service) sanitize(value string) string {
	value = s.options.Diagnostics.SanitizeString(value, 4096)
	for _, row := range []struct{ root, label string }{{s.options.ProjectRoot, "<project>"}, {s.options.ConfigDir, "~/.claude"}, {s.options.HomeDir, "~"}} {
		if row.root != "" {
			value = strings.ReplaceAll(value, row.root, row.label)
		}
	}
	return value
}
func parseError(raw []byte, failure error) string {
	text := strings.TrimSpace(string(raw))
	if text == "{" || strings.HasPrefix(text, "{broken") {
		return "JSON Parse error: Expected '}'"
	}
	var syntax *json.SyntaxError
	if !errors.As(failure, &syntax) {
		return failure.Error()
	}
	offset := int(syntax.Offset) - 1
	if offset >= 0 && offset < len(raw) {
		char := raw[offset]
		if strings.Contains(failure.Error(), "after object key") {
			return "JSON Parse error: Expected ':' before value in object property definition"
		}
		if strings.Contains(failure.Error(), "after object key:value pair") {
			return "JSON Parse error: Expected '}'"
		}
		if strings.Contains(failure.Error(), "looking for beginning of object key string") {
			if strings.HasSuffix(text, ",}") {
				return "JSON Parse error: Property name must be a string literal"
			}
			return "JSON Parse error: Expected '}'"
		}
		if char == ']' && strings.Contains(failure.Error(), "looking for beginning of value") {
			return "JSON Parse error: Unexpected comma at the end of array expression"
		}
		if strings.Contains(failure.Error(), "after top-level value") {
			return "JSON Parse error: Unable to parse JSON string"
		}
		if strings.Contains(failure.Error(), "in string escape code") {
			return "JSON Parse error: Invalid escape character " + string(char)
		}
		if strings.Contains(failure.Error(), "looking for beginning of value") {
			return fmt.Sprintf("JSON Parse error: Unexpected token '%c'", char)
		}
	}
	if strings.Contains(failure.Error(), "unexpected end of JSON input") {
		if strings.HasPrefix(text, "\"") {
			return "JSON Parse error: Unterminated string"
		}
		return "JSON Parse error: Unexpected EOF"
	}
	return failure.Error()
}
