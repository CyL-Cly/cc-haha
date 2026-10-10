package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"

func (c *Controller) UsesDiagnostics(process *diagnostics.Process) bool {
	return c != nil && c.options.Diagnostics == process && c.options.UserContext.UsesDiagnostics(process)
}
