package mcpconfig

import (
	"context"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) AllConfigurations(ctx context.Context, organization protocol.JSONObject) (Result, error) {
	return s.AllConfigurationsAwait(ctx, func(context.Context) (protocol.JSONObject, error) { return organization, nil })
}

// The producer is started by the owner before this call. One observation is
// shared by plugin dedup and the final lowest-precedence organization merge.
func (s *Service) AllConfigurationsAwait(ctx context.Context, awaitOrganization func(context.Context) (protocol.JSONObject, error)) (Result, error) {
	organization := protocol.NewJSONObject()
	code, err := s.ConfigurationsAwait(ctx, protocol.NewJSONObject(), func(ctx context.Context) (protocol.JSONObject, error) {
		var err error
		organization, err = awaitOrganization(ctx)
		return organization, err
	})
	if err != nil || s.EnterpriseExists() {
		return code, err
	}
	settings, err := s.options.Settings(ctx)
	if err != nil {
		return empty(), err
	}
	policy, err := s.options.Policy(ctx)
	if err != nil {
		return empty(), err
	}
	allowed := protocol.NewJSONObject()
	for _, field := range organization.OwnProperties() {
		config := object(field.Value)
		if config.String("type") == "sdk" || Allowed(name(field), config, settings, policy) {
			_ = allowed.SetPropertyJSON(field.NameJSON, field.Value)
		}
	}
	manual := protocol.NewJSONObject()
	for _, field := range code.Servers.OwnProperties() {
		enabled, err := s.options.Enabled(ctx, name(field), object(field.Value))
		if err != nil {
			return empty(), err
		}
		if enabled {
			_ = manual.SetPropertyJSON(field.NameJSON, field.Value)
		}
	}
	servers, _ := Dedup(allowed, manual, false)
	servers.Assign(code.Servers)
	return Result{Servers: servers, Errors: code.Errors}, nil
}
