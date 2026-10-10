package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func (m *MainPrint) nativeMaybeMarkProjectOnboardingComplete(ctx context.Context, invocation skillcommands.Invocation) error {
	scope := invocation.NativeScope
	if !scope.BelongsTo(m.Owner) || m.Global == nil || m.options.Native == nil {
		return cooperative.ErrRestricted
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := m.originalProjectConfigKey(scope)
	if err != nil {
		return err
	}
	current, err := m.Global.GetProject(key)
	if err != nil {
		return err
	}
	if planRawTruthy(current.Get("hasCompletedProjectOnboarding")) {
		return nil
	}
	cwd := m.state.Snapshot().CWD
	// Original existsSync accepts a directory too. Both onboarding steps are
	// evaluated: an empty workspace always has an unfinished enabled step.
	_, existsErr := m.options.Native.stat(filepath.Join(cwd, "CLAUDE.md"))
	entries, readErr := m.options.Native.readDir(cwd)
	empty := readErr == nil && len(entries) == 0 || errors.Is(readErr, fs.ErrNotExist)
	if empty || existsErr != nil {
		return nil
	}
	return m.Global.SaveProject(key, func(project *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := project.Clone()
		next.Set("hasCompletedProjectOnboarding", json.RawMessage("true"))
		return &next, nil
	})
}
