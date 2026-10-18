package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"

// ReplaceNativeMCPPrompts publishes only AppState.mcp.commands. Source's MCP
// connection updates leave the already mounted local/plugin command objects
// and plugin metadata alone, even when a plugin operation cleared disk memos.
func (s *PrintRegistry) ReplaceNativeMCPPrompts(prompts []skillcommands.Definition, headless bool) {
	if s.remoteSafe {
		prompts = nil
	}
	current := make([]skillcommands.Definition, 0, len(s.prompts)+len(prompts))
	for _, prompt := range s.prompts {
		metadata := prompt.Metadata()
		if string(metadata.Get("isMcp")) != "true" && metadata.String("source") != "mcp" {
			current = append(current, prompt)
		}
	}
	current = append(current, prompts...)
	// REPL's useMergedCommands uses uniqBy(localCommands + mcp.commands),
	// retaining the first local object on a collision. Headless print keeps
	// its ordinary combined array and resolves a command by the first match.
	if !headless && len(prompts) > 0 {
		seen := map[string]bool{}
		merged := current[:0]
		for _, prompt := range current {
			if !seen[prompt.Name()] {
				merged = append(merged, prompt)
				seen[prompt.Name()] = true
			}
		}
		current = merged
	}
	s.ReplaceNativePrompts(current, s.catalog.Plugins, headless)
}
