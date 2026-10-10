package builtintools

import "github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"

// The default path must retain its original permissions semantics. Admission
// requires real producers for the complete external native registry; explicit
// subsets remain available when a caller has deliberately bound fewer tools.
func (s *Service) HasDefaultCatalogBindings() bool {
	if s == nil || s.options.RunAgent == nil || s.options.RunBackgroundAgent == nil || s.options.AgentDefinitions == nil || s.options.AgentPrompt == nil || s.options.GetTodos == nil || s.options.SetTodos == nil || s.options.TaskStore == nil || s.options.TaskListID == nil || s.options.FileSkills == nil {
		return false
	}
	if !s.HasPlanBindings() || !s.HasWebFetchBindings() || !s.HasWebSearchBindings() || !s.HasSkillBindings() || !s.HasWorktreeBindings() || !s.HasMCPResourceBindings() || !s.HasToolSearchBindings() || !s.HasBackgroundTaskBindings() || !s.HasWorkflowBindings() {
		return false
	}
	// The enabled image profile retains both tools. An unbound token authority
	// remains explicit rather than reducing the Source default registry.
	if config := imagegeneration.RuntimeConfig(s.options.Environment); config != nil {
		if !s.HasImageBindings() {
			return false
		}
	}
	return true
}
