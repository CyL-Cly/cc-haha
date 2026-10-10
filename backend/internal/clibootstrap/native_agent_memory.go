package clibootstrap

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func (m *MainPrint) nativeAgentMemoryPrompt(scope *cooperative.Scope, name, memory string) (string, error) {
	return nativeAgentMemoryPrompt(m.Owner, m.options.Native, m.Startup, scope, name, memory)
}

func nativeAgentMemoryPrompt(owner *cooperative.Owner, n *NativeBoundary, startup *clistartupenv.Service, scope *cooperative.Scope, name, memory string) (string, error) {
	if !scope.BelongsTo(owner) {
		return "", cooperative.ErrRestricted
	}
	name = strings.ReplaceAll(name, ":", "-")
	base := n.Environment["CLAUDE_CODE_REMOTE_MEMORY_DIR"]
	if base == "" {
		base = n.Environment["CLAUDE_CONFIG_DIR"]
	}
	directory, note := "", ""
	switch memory {
	case "user":
		directory = filepath.Join(base, "agent-memory", name)
		note = "- Since this memory is user-scope, keep learnings general since they apply across all projects"
	case "project":
		directory = filepath.Join(n.CWD, ".claude", "agent-memory", name)
		note = "- Since this memory is project-scope and shared with your team via version control, tailor your memories to this project"
	case "local":
		directory = filepath.Join(n.CWD, ".claude", "agent-memory-local", name)
		if remote := n.Environment["CLAUDE_CODE_REMOTE_MEMORY_DIR"]; remote != "" {
			canonical := usercontext.CanonicalGitRoot(mainSettingsFiles{n: n}, filepath.EvalSymlinks, n.CWD)
			if canonical == "" {
				canonical = n.CWD
			}
			directory = filepath.Join(remote, "projects", projectpath.Sanitize(canonical), "agent-memory-local", name)
		}
		note = "- Since this memory is local-scope (not checked into version control), tailor your memories to this project and machine"
	default:
		return "", mainCap("Agent memory scope " + memory)
	}
	directory += string(filepath.Separator)
	// Copy the environment so scope guidance never changes the parent's memory.
	values := protocol.NewJSONObject()
	for key, value := range n.Environment {
		values.Set(key, quoteMain(value))
	}
	guidelines := n.Environment["CLAUDE_COWORK_MEMORY_EXTRA_GUIDELINES"]
	if nativeAgentJSTrim(guidelines) != "" {
		note += "\n" + guidelines
	}
	values.Set("CLAUDE_COWORK_MEMORY_EXTRA_GUIDELINES", quoteMain(note))
	env, err := clistartupenv.NewEnvironment(values)
	if err != nil {
		return "", err
	}
	settings, err := startup.GetInitialSettings()
	if err != nil || settings == nil {
		return "", err
	}
	builder := systemprompt.AutoMemory{Directory: func() (string, error) { return directory, nil }, Mkdir: func(path string) error { return (mainSettingsFiles{n: n}).MkdirAll(path) }}
	behavior, err := builder.Load(context.Background(), env, *settings)
	if err != nil || behavior == nil {
		return "", err
	}
	text := strings.Replace(*behavior, "# auto memory\n", "# Persistent Agent Memory\n", 1)
	data, err := n.readFile(filepath.Join(directory, "MEMORY.md"))
	if err != nil || nativeAgentJSTrim(string(protocol.DecodeJavaScriptUTF8(data))) == "" {
		return text + "\n## MEMORY.md\n\nYour MEMORY.md is currently empty. When you save new memories, they will appear here.", nil
	}
	content := usercontext.TruncateAgentMemoryEntrypoint(string(protocol.DecodeJavaScriptUTF8(data)))
	joined, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quoteMain(text + "\n## MEMORY.md\n\n"), content}, "")
	if err != nil {
		return "", err
	}
	return decodeNativeModel(joined)
}
