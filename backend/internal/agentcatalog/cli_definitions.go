package agentcatalog

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// InstallCLIDefinitions accepts the already parsed native --agents producer.
// Discovery and active-source precedence remain in the same catalog.
func (s *Service) InstallCLIDefinitions(scope *cooperative.Scope, definitions []Definition) error {
	if err := s.check(scope); err != nil {
		return err
	}
	for _, definition := range definitions {
		if definition.Source != "flagSettings" || definition.Name == "" && string(definition.Fields.Get("agentType")) != `""` || definition.SystemPrompt == "" {
			return cap("parsed CLI Agent definition identity")
		}
	}
	s.cli = append([]Definition{}, definitions...)
	for i := range s.cli {
		s.cli[i].Fields = s.cli[i].Fields.Clone()
		s.cli[i].Frontmatter = s.cli[i].Frontmatter.Clone()
	}
	s.cache = nil
	return nil
}
