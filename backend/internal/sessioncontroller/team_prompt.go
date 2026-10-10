package sessioncontroller

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// EnqueueTeamPromptOnOwner is the original leader inbox enqueue. It preserves
// raw UTF-16 content and uses the existing main command queue/UUID producer.
func (c *Controller) EnqueueTeamPromptOnOwner(scope *cooperative.Scope, content json.RawMessage, meta bool) error {
	if _, err := c.todosController(scope); err != nil {
		return err
	}
	if (!c.paneInboxRole() && (c.options.TeamContext != nil || c.options.InProcessTeammateContext != nil || c.leaderTeamName == "")) || !c.active || !c.options.PublicPrint || !c.options.NativeTools {
		return cooperative.ErrRestricted
	}
	if len(content) == 0 || content[0] != '"' || !json.Valid(content) {
		return cooperative.ErrRestricted
	}
	return c.enqueueOwned(scope, command{Content: append(json.RawMessage(nil), content...), UUID: quote(c.options.UUID()), Meta: meta, Mode: "prompt"})
}
