package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
)

func (m *MainPrint) nativeSDKMCPControl(scope *cooperative.Scope, request protocol.JSONObject) (sdkhost.NativeSDKMCPControlResult, error) {
	if !scope.BelongsTo(m.Owner) || m.mcpTools == nil {
		return sdkhost.NativeSDKMCPControlResult{}, cooperative.ErrRestricted
	}
	// mcpChangesPromise.then(doWork,doWork): failed changes never poison the next
	// request, and owner futures serialize the real native-await boundary.
	previous := m.sdkMCPControlTail
	tail, err := sdkhost.NewOwnerFuture[struct{}](m.Owner)
	if err != nil {
		return sdkhost.NativeSDKMCPControlResult{}, err
	}
	m.sdkMCPControlTail = tail
	if previous != nil {
		_, _ = previous.Await(scope)
	}
	defer func() { _ = tail.Resolve(scope, struct{}{}) }()
	if request.String("subtype") == "mcp_set_servers" {
		configs, err := m.sdkMCPState.Configurations(scope)
		if err != nil {
			return sdkhost.NativeSDKMCPControlResult{}, err
		}
		result, err := m.mcpTools.ReconcileSDKServers(scope, configs, sdkMCPControlObject(request.Get("servers")))
		if err != nil {
			return sdkhost.NativeSDKMCPControlResult{}, err
		}
		if err = m.sdkMCPState.ReplaceConfigurations(scope, result.Configurations); err != nil {
			return sdkhost.NativeSDKMCPControlResult{}, err
		}
		if err = m.Host.RefreshNativeToolCatalog(scope); err != nil {
			return sdkhost.NativeSDKMCPControlResult{}, err
		}
		return sdkhost.NativeSDKMCPControlResult{Response: result.Response, RefreshSDK: result.SDKChanged}, nil
	}
	config, err := m.mcpTools.ServerConfiguration(scope, request.String("serverName"))
	if err != nil {
		return sdkhost.NativeSDKMCPControlResult{}, err
	}
	if config == nil {
		return sdkhost.NativeSDKMCPControlResult{}, errors.New("Server not found: " + request.String("serverName"))
	}
	if request.String("subtype") == "mcp_toggle" {
		enabled := openaiauth.AuthTruthy(request.Get("enabled"))
		alreadyPersisted := openaiauth.AuthTruthy(request.Get("alreadyPersisted"))
		if alreadyPersisted {
			enabled, err = m.nativeSettings.MCPExecutionEnabled(context.Background(), request.String("serverName"), *config)
			if err != nil {
				return sdkhost.NativeSDKMCPControlResult{}, err
			}
		} else {
			editor, err := m.nativeSettings.MCPEditor(m.mcpConfigurations)
			if err != nil {
				return sdkhost.NativeSDKMCPControlResult{}, err
			}
			if err = editor.SetEnabled(context.Background(), request.String("serverName"), enabled); err != nil {
				return sdkhost.NativeSDKMCPControlResult{}, err
			}
		}
		if !enabled {
			err = m.mcpTools.DisableServer(scope, request.String("serverName"))
			if err == nil {
				err = m.Host.RefreshNativeToolCatalog(scope)
			}
			return sdkhost.NativeSDKMCPControlResult{}, err
		}
	}
	err = m.mcpTools.ReconnectSDKControl(context.Background(), scope, request.String("serverName"), request.String("subtype") == "mcp_toggle")
	refreshErr := m.Host.RefreshNativeToolCatalog(scope)
	if err == nil {
		err = refreshErr
	}
	return sdkhost.NativeSDKMCPControlResult{}, err
}

func sdkMCPControlObject(value json.RawMessage) protocol.JSONObject {
	var object protocol.JSONObject
	_ = object.UnmarshalJSON(value)
	return object
}
