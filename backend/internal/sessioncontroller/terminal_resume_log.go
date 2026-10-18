package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (c *Controller) BeginTerminalFullLogOnOwner(scope *cooperative.Scope, log protocol.JSONObject) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
	if err := c.checkTerminalResume(scope, nil); err != nil {
		return nil, err
	}
	return c.resume.BeginTerminalFullLog(scope, log)
}
