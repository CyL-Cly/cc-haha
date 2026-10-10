package clistartupenv

import "github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"

func (s *Service) UsesDiagnostics(process *diagnostics.Process) bool {
	return s != nil && s.options.Diagnostics == process
}
