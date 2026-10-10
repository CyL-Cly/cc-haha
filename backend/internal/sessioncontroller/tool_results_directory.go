package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"path/filepath"
)

// ToolResultsDirectoryOnOwner follows Source getToolResultsDir's OriginalCWD
// project key. Its semantics are intentionally distinct from sidechain resume
// locators, which may adopt an indexed SessionProjectDir.
func (c *Controller) ToolResultsDirectoryOnOwner(scope *cooperative.Scope) (string, error) {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return "", cooperative.ErrRestricted
	}
	c.writer.mu.Lock()
	defer c.writer.mu.Unlock()
	snapshot := c.options.Bootstrap.Snapshot()
	return filepath.Join(c.writer.options.ConfigDir, "projects", sanitize(snapshot.OriginalCWD), c.SessionID(), "tool-results"), nil
}
