package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

type nativeWorkflowSynthetic struct {
	Schema   json.RawMessage
	Validate func(context.Context, protocol.JSONObject) error
}

func (m *MainPrint) initializeWorkflowSynthetic(scope *cooperative.Scope) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	d := m.options.DetachedAgent
	if d == nil || d.synthetic == nil {
		return nil
	}
	if !m.options.Native.NativeProcess || m.options.QuerySource != "workflow_agent" {
		return cooperative.ErrRestricted
	}
	resources, e := m.Controller.BorrowNativeCopyBinding(m.Owner)
	if e != nil {
		return e
	}
	binding, e := sdkhost.NewNativeSyntheticOutput(m.Owner, resources, d.synthetic.Schema, d.synthetic.Validate)
	if e != nil {
		return e
	}
	m.nativeSynthetic = binding
	return nil
}
