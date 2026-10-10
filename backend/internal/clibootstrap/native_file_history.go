package clibootstrap

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

func (m *MainPrint) initializeNativeFileHistory(scope *cooperative.Scope) error {
	if !m.options.Native.NativeProcess {
		return nil
	}
	service, e := m.Controller.BindNativeFileHistory(scope, func(scope *cooperative.Scope) (bool, error) {
		if !scope.BelongsTo(m.Owner) {
			return false, cooperative.ErrRestricted
		}
		if m.options.AgentID != "" && m.teamWorker == nil || nativeEnvTruthy(m.options.Native.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"]) {
			return false, nil
		}
		if !m.options.Interactive {
			return nativeEnvTruthy(m.options.Native.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"]), nil
		}
		// Source fileHistoryEnabled samples the live global preference in
		// interactive sessions; SDK callers keep their explicit opt-in.
		global, e := m.Global.Get()
		if e != nil {
			return false, e
		}
		return string(global.Get("fileCheckpointingEnabled")) != "false", nil
	}, m.options.Native.Now, func() (string, error) { return uuidMain(), nil }, sessionrewind.NativeFiles{ConfigHome: m.options.Native.Environment["CLAUDE_CONFIG_DIR"]})
	if e != nil {
		return e
	}
	m.fileHistory = service
	return nil
}
func (m *MainPrint) nativeTrackFileHistory(scope *cooperative.Scope, path string, parent json.RawMessage) error {
	if !scope.BelongsTo(m.Owner) {
		return cooperative.ErrRestricted
	}
	// Original createSubagentContext supplies a no-op updateFileHistoryState.
	// Its async Track still settles; no root or shadow child state is mutated.
	if m.options.AgentID != "" && m.teamWorker == nil {
		return scope.AwaitFulfilled()
	}
	if m.fileHistory == nil {
		return mainCap("actual file history producer")
	}
	if len(parent) == 0 {
		return mainCap("actual file history tool parent UUID")
	}
	if e := m.fileHistory.Track(scope, path, parent); e != nil {
		return e
	}
	return scope.AwaitFulfilled()
}
