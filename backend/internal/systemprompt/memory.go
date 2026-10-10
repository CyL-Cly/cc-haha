package systemprompt

import (
	"context"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// AutoMemory binds the real shared memory path and native mkdir operation.
// The memory entrypoint content belongs in user context, not these sections.
type AutoMemory struct {
	Directory           func() (string, error)
	Mkdir               func(string) error
	SkipIndex           func() bool
	SearchPastContext   func() bool
	ProjectDirectory    func() string
	EmbeddedSearchTools func() bool
	REPLMode            func() bool
}

func AutoMemoryEnabled(environment *clistartupenv.Environment, settings protocol.JSONObject) bool {
	if envEnabled(environment, "CLAUDE_CODE_DISABLE_AUTO_MEMORY") {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(rawString(environment.Get("CLAUDE_CODE_DISABLE_AUTO_MEMORY"))))
	if value == "0" || value == "false" || value == "no" || value == "off" {
		return true
	}
	if envEnabled(environment, "CLAUDE_CODE_SIMPLE") {
		return false
	}
	if envEnabled(environment, "CLAUDE_CODE_REMOTE") && rawString(environment.Get("CLAUDE_CODE_REMOTE_MEMORY_DIR")) == "" {
		return false
	}
	if value := settings.Get("autoMemoryEnabled"); value != nil {
		return string(value) == "true"
	}
	return true
}

func (m *AutoMemory) Load(ctx context.Context, environment *clistartupenv.Environment, settings protocol.JSONObject) (*string, error) {
	if !AutoMemoryEnabled(environment, settings) {
		return nil, nil
	}
	if m.Directory == nil || m.Mkdir == nil {
		return nil, errors.New("auto memory requires native path resolution and mkdir")
	}
	directory, err := m.Directory()
	if err != nil {
		return nil, err
	}
	// Original mkdir logs failures and continues prompt building. The eventual
	// Write tool reports a real permissions error at its normal execution seam.
	_ = m.Mkdir(directory)
	base := memoryNormalLines
	if enabled(m.SkipIndex) {
		base = memorySkipLines
	}
	lines := append([]string(nil), base...)
	for i, line := range lines {
		lines[i] = strings.ReplaceAll(line, "__AUTO_MEMORY_DIRECTORY__", directory)
	}
	guidelines := rawString(environment.Get("CLAUDE_COWORK_MEMORY_EXTRA_GUIDELINES"))
	if strings.TrimSpace(guidelines) != "" {
		lines = append(append(lines[:len(lines)-1], guidelines), "")
	}
	if enabled(m.SearchPastContext) {
		if m.ProjectDirectory == nil {
			return nil, errors.New("past context search requires actual session project directory")
		}
		memorySearch := "Grep with pattern=\"<search term>\" path=\"" + directory + "\" glob=\"*.md\""
		transcriptSearch := "Grep with pattern=\"<search term>\" path=\"" + m.ProjectDirectory() + "/\" glob=\"*.jsonl\""
		if enabled(m.EmbeddedSearchTools) || enabled(m.REPLMode) {
			memorySearch = "grep -rn \"<search term>\" " + directory + " --include=\"*.md\""
			transcriptSearch = "grep -rn \"<search term>\" " + m.ProjectDirectory() + "/ --include=\"*.jsonl\""
		}
		lines = append(lines, "## Searching past context", "", "When looking for past context:", "1. Search topic files in your memory directory:", "```", memorySearch, "```", "2. Session transcript logs (last resort — large files, slow):", "```", transcriptSearch, "```", "Use narrow search terms (error messages, file paths, function names) rather than broad keywords.", "")
	}
	return pointer(strings.Join(lines, "\n")), nil
}
