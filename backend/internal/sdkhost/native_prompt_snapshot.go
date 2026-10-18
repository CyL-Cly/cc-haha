package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"

// NativePromptDefinitions is the mounted command snapshot supplied to actual
// ToolUseContext consumers. Copy the slice while retaining each immutable
// definition and its captured prompt, as Source options.commands does.
func (s *PrintRegistry) NativePromptDefinitions() []skillcommands.Definition {
	if s == nil {
		return nil
	}
	return append([]skillcommands.Definition{}, s.prompts...)
}
