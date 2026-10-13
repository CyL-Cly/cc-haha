package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func (m *MainPrint) initializeNativeSDKMCPState() error {
	if m.sdkMCPState != nil {
		return nil
	}
	var err error
	m.sdkMCPState, err = sdkhost.NewNativeSDKMCPState(m.Owner)
	return err
}

func (m *MainPrint) nativeSDKMCPConfigurations(context.Context) (protocol.JSONObject, error) {
	return sdkWorktreeOwnerCall(context.Background(), m, func(scope *cooperative.Scope) (protocol.JSONObject, error) {
		return m.sdkMCPState.ConfigurationsForDiscovery(scope)
	})
}

func (m *MainPrint) nativeSDKMCPBindings(_ context.Context, name string, _ protocol.JSONObject) (*mcpclient.SDKControlBindings, error) {
	return &mcpclient.SDKControlBindings{Send: func(_ context.Context, message protocol.JSONObject) (json.RawMessage, error) {
		// Source sendMcpMessage owns its pending request until response/EOF.
		// A model cancellation must not cancel that control-message producer.
		copied := message.Clone()
		return sdkWorktreeOwnerCall(context.Background(), m, func(scope *cooperative.Scope) (json.RawMessage, error) {
			future, err := m.sdkMCPState.Send(scope, name, copied)
			if err != nil {
				return nil, err
			}
			return future.Await(scope)
		})
	}}, nil
}

func (m *MainPrint) prepareNativeSDKMCPTurn(scope *cooperative.Scope) error {
	if m.mcpTools == nil || m.options.DetachedAgent != nil {
		return nil
	}
	configurations, err := m.sdkMCPState.Configurations(scope)
	if err != nil {
		return err
	}
	changed, err := m.mcpTools.SDKNeedsRefresh(scope, configurations)
	if err != nil || !changed {
		return err
	}
	if err := m.mcpTools.Discover(scope); err != nil {
		return err
	}
	return m.Host.RefreshNativeToolCatalog(scope)
}

func (m *MainPrint) deliverNativeSDKMCPMessage(scope *cooperative.Scope, name string, message protocol.JSONObject) error {
	if m.mcpTools == nil {
		return nil
	}
	return m.mcpTools.DeliverSDKMessage(scope, name, message)
}
