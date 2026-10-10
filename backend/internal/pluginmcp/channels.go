package pluginmcp

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpb"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) UnconfiguredChannels(ctx context.Context, plugin protocol.JSONObject) ([]protocol.JSONObject, error) {
	result := []protocol.JSONObject{}
	manifest := object(plugin.Get("manifest"))
	var channels []protocol.JSONObject
	_ = json.Unmarshal(manifest.Get("channels"), &channels)
	for _, channel := range channels {
		schema := object(channel.Get("userConfig"))
		if !truthy(channel.Get("userConfig")) || len(schema.OwnProperties()) == 0 {
			continue
		}
		saved, err := s.LoadServerUserConfig(ctx, plugin.String("repository"), channel.String("server"))
		if err != nil {
			return nil, err
		}
		values := protocol.NewJSONObject()
		if saved != nil {
			values = *saved
		}
		valid, _ := mcpb.ValidateUserConfig(values, schema)
		if valid {
			continue
		}
		display := channel.Get("displayName")
		if display == nil || string(display) == "null" {
			display = channel.Get("server")
		}
		result = append(result, record(field("server", channel.String("server")), protocol.JSONField{Name: "displayName", Value: display}, field("configSchema", schema)))
	}
	return result, nil
}
