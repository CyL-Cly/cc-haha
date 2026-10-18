package clistartupenv

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// NativeMigrationSources binds the actual startup source cache and its writer.
// It cannot be minted by a finite settings service or attached to a different
// global/configuration owner. All ordinary getter host checks still apply.
type NativeMigrationSources struct {
	owner    *cooperative.Owner
	startup  *Service
	settings *clisettings.Service
	global   *cliglobalconfig.Service
}

func (s *Service) BindNativeMigrationSources(scope *cooperative.Scope, owner *cooperative.Owner, settings *clisettings.Service, state *clisettings.State, global *cliglobalconfig.Service) (*NativeMigrationSources, error) {
	if s == nil || scope == nil || !scope.BelongsTo(owner) || !resolvedNativePolicyProfile(s.options.Host.Profile) || settings == nil || state == nil || global == nil || s.options.Settings != settings || s.options.SettingsState != state || s.options.Global != global || !settings.UsesNativeUserSettingsPath(state) {
		return nil, cooperative.ErrRestricted
	}
	bound := &NativeMigrationSources{owner: owner, startup: s, settings: settings, global: global}
	return bound, bound.Check(scope, owner, global)
}

func (bound *NativeMigrationSources) Check(scope *cooperative.Scope, owner *cooperative.Owner, global *cliglobalconfig.Service) error {
	if bound == nil || scope == nil || !scope.BelongsTo(owner) || owner != bound.owner || global != bound.global || bound.startup == nil || bound.startup.options.Global != global || bound.startup.options.Settings != bound.settings {
		return cooperative.ErrRestricted
	}
	if err := scope.RegisterPromiseReaction(func(*cooperative.Owner) error { return nil }); err != nil {
		return err
	}
	return bound.startup.checkHostControls()
}

func (bound *NativeMigrationSources) Source(scope *cooperative.Scope, name string) (*protocol.JSONObject, error) {
	if bound == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := bound.Check(scope, bound.owner, bound.global); err != nil {
		return nil, err
	}
	return bound.startup.GetSettingsForSource(name)
}

func (bound *NativeMigrationSources) Merged(scope *cooperative.Scope) (*protocol.JSONObject, error) {
	if bound == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := bound.Check(scope, bound.owner, bound.global); err != nil {
		return nil, err
	}
	return bound.startup.GetInitialSettings()
}

func (bound *NativeMigrationSources) Update(scope *cooperative.Scope, source string, patch clisettings.Patch, writer clisettings.NativeSourceWriterBindings) (clisettings.WriteResult, error) {
	if bound == nil {
		return clisettings.WriteResult{}, cooperative.ErrRestricted
	}
	if err := bound.Check(scope, bound.owner, bound.global); err != nil {
		return clisettings.WriteResult{}, err
	}
	return bound.settings.UpdateNativeEditableSource(source, "", patch, writer)
}
