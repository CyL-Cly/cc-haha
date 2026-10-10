package sessioncontroller

import "github.com/NanmiCoder/cc-haha/backend/internal/cooperative"

// AdoptNativeResumedTranscript ports adoptResumedSessionFile's pointer change
// and skipTitleRefresh stamp after native restoreWorktreeForResume. It never
// creates a missing transcript and leaves the original cached metadata intact.
func (c *Controller) AdoptNativeResumedTranscript(scope *cooperative.Scope) error {
	if c == nil || c.options.Bootstrap == nil || !c.options.NativeTools || c.options.NativeWorktreeResume == nil {
		return cooperative.ErrRestricted
	}
	if e := c.options.Bootstrap.CheckScope(scope); e != nil {
		return e
	}
	c.writer.mu.Lock()
	defer c.writer.mu.Unlock()
	c.writer.path = c.writer.target()
	return c.writer.refreshMetadataWithTitleRefresh(false, true)
}
