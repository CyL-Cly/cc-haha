package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

func (m *MainPrint) nativeSpinnerVerb(scope *cooperative.Scope) (string, error) {
	if m == nil || !scope.BelongsTo(m.Owner) {
		return "", cooperative.ErrRestricted
	}
	settings, err := m.Startup.GetInitialSettings()
	if err != nil {
		return "", err
	}
	verbs, err := teamspawn.SpinnerVerbs(*settings)
	if err != nil {
		return "", err
	}
	value, err := teamspawn.Sample(verbs)
	var verb string
	if err == nil {
		err = json.Unmarshal(value, &verb)
	}
	return verb, err
}

func (m *MainPrint) SpinnerVerb(ctx context.Context) (string, error) {
	var verb string
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		verb, err = m.nativeSpinnerVerb(scope)
		return err
	})
	return verb, err
}
