package clibootstrap

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The frozen distribution has an empty GrowthBook key. Its original getter
// still checks ant-only explicit env and global overrides before that gate;
// a cached remote payload alone must never enable the external build.
type nativeRemoteBackendState struct {
	overrides *protocol.JSONObject
}

func (m *MainPrint) NativeCLIRemoteBackendEnabled(ctx context.Context) (bool, error) {
	if m == nil || m.Owner == nil {
		return false, cooperative.ErrRestricted
	}
	enabled := false
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		var err error
		enabled, err = m.nativeCLIRemoteBackendOnOwner(scope)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return enabled, err
}
func (m *MainPrint) nativeCLIRemoteBackendOnOwner(scope *cooperative.Scope) (bool, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) || m.options.Native == nil || !m.options.Native.NativeProcess {
		return false, cooperative.ErrRestricted
	}
	env := m.nativeOpenAIEnvironment()
	if m.nativeRemoteBackend == nil {
		m.nativeRemoteBackend = &nativeRemoteBackendState{}
		if env["USER_TYPE"] == "ant" {
			var overrides protocol.JSONObject
			if overrides.UnmarshalJSON([]byte(env["CLAUDE_INTERNAL_FC_OVERRIDES"])) == nil && overrides.IsObject() {
				m.nativeRemoteBackend.overrides = &overrides
			}
		}
	}
	if overrides := m.nativeRemoteBackend.overrides; overrides != nil && overrides.Get("tengu_remote_backend") != nil {
		return nativeSettingsTruthy(overrides.Get("tengu_remote_backend")), nil
	}
	if env["USER_TYPE"] != "ant" {
		return false, nil
	}
	global, err := m.Global.Get()
	if err != nil {
		return false, err
	}
	if global != nil {
		overrides, _ := global.Object("growthBookOverrides")
		return nativeSettingsTruthy(overrides.Get("tengu_remote_backend")), nil
	}
	return false, nil
}

func (m *MainPrint) nativeRemoteCatalogEnabled(ctx context.Context, scopes ...*cooperative.Scope) (bool, error) {
	if m.options.Native == nil || !m.options.Native.NativeProcess || m.options.Arguments.Remote == nil {
		return false, nil
	}
	if len(scopes) > 0 {
		return m.nativeCLIRemoteBackendOnOwner(scopes[0])
	}
	return m.NativeCLIRemoteBackendEnabled(ctx)
}
