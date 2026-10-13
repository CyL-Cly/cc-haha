package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) seedNativeCLITitle(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) || m.Controller == nil {
		return cooperative.ErrRestricted
	}
	if m.agentChild || m.teamWorker != nil {
		return nil
	}
	if title := jsTrim(m.options.Arguments.Name); title != "" {
		m.Controller.Writer().RestoreMetadata(protocol.NewJSONObject(protocol.JSONField{Name: "customTitle", Value: quoteMain(title)}))
	}
	return nil
}
