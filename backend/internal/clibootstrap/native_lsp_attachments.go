package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (m *MainPrint) nativeLSPAttachments(scope *cooperative.Scope, agentID string) ([]queryloop.Message, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if agentID != "" || m.nativeLSP == nil || m.nativeLSPLife.borrowed {
		return nil, nil
	}
	available, err := m.Host.HasRegisteredTool(scope, "Bash")
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, nil
	}
	rows := m.nativeLSP.TakeDiagnostics()
	out := []queryloop.Message{}
	for _, row := range rows {
		out = append(out, queryloop.Message{Record: m.factory.CreateAttachment(rawMain(row))})
	}
	return out, nil
}
