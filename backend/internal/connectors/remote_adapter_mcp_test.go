package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestRemoteBridgeReadyComesFromActualNativeMCPToolsDiscovery(t *testing.T) {
	for _, kind := range []string{"tools", "zero-tools", "401"} {
		t.Run(kind, func(t *testing.T) {
			home, row := remoteFixture(t, "api-key")
			row.Recipe.Auth.In = "header"
			row.Recipe.Auth.Name = "Authorization"
			row.Recipe.Auth.Prefix = "Bearer "
			var mu sync.Mutex
			methods := []string{}
			host := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fake-real-protocol-key" {
					t.Error("saved API key did not reach actual transport")
				}
				if kind == "401" {
					w.WriteHeader(401)
					return
				}
				if r.Method != "POST" {
					w.WriteHeader(405)
					return
				}
				var message struct {
					ID     json.RawMessage
					Method string
				}
				if json.NewDecoder(r.Body).Decode(&message) != nil {
					w.WriteHeader(400)
					return
				}
				mu.Lock()
				methods = append(methods, message.Method)
				mu.Unlock()
				if len(message.ID) == 0 {
					w.WriteHeader(202)
					return
				}
				result := any(nil)
				switch message.Method {
				case "initialize":
					result = map[string]any{"protocolVersion": mcpclient.LatestProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "loopback", "version": "1"}}
				case "tools/list":
					tools := []any{}
					if kind == "tools" {
						tools = append(tools, map[string]any{"name": "actual_list", "inputSchema": map[string]any{"type": "object"}})
					}
					result = map[string]any{"tools": tools}
				default:
					t.Error("unexpected RPC method", message.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.ID), "result": result})
			}))
			defer host.Close()
			row.Recipe.Endpoint = host.URL + "/mcp"
			options := record(field("apiKey", "fake-real-protocol-key"))
			deps := RemoteDependencies{
				Probe: func(ctx context.Context, name string, config protocol.JSONObject) (RemoteProbe, error) {
					headers := nested(config.Get("headers"))
					client, err := mcpclient.Connect(ctx, mcpclient.Config{Type: config.String("type"), URL: config.String("url"), Headers: map[string]string{"Authorization": headers.String("Authorization")}}, mcpclient.Options{Name: name, CWD: home, Environment: map[string]string{}, HTTPClient: host.Client()})
					if err != nil {
						var auth *mcpclient.AuthError
						if errors.As(err, &auth) {
							return RemoteProbe{"needs-auth", 0}, nil
						}
						return RemoteProbe{}, err
					}
					defer client.Close()
					result, err := client.ListTools(ctx, nil)
					if err != nil {
						return RemoteProbe{}, err
					}
					var tools []protocol.JSONObject
					if err = json.Unmarshal(result.Get("tools"), &tools); err != nil {
						return RemoteProbe{}, err
					}
					return RemoteProbe{"connected", len(tools)}, nil
				},
				OAuth: func(context.Context, string, protocol.JSONObject, func(string) error) error {
					return errors.New("no OAuth was authorized in protocol fixture")
				},
				Disconnect:  func(context.Context, string, protocol.JSONObject) error { return nil },
				LoadOptions: func(context.Context, string) (protocol.JSONObject, error) { return options.Clone(), nil },
				SaveOptions: func(_ context.Context, _ string, value, schema protocol.JSONObject) error {
					options = value.Clone()
					return nil
				},
				DeleteOptions:       func(context.Context, string) error { options = protocol.NewJSONObject(); return nil },
				ClearOAuth:          func(context.Context, string, protocol.JSONObject) error { return nil },
				Environment:         func(string) (string, bool) { return "", false },
				PluginDataDirectory: func(context.Context, string) (string, error) { return filepath.Join(home, "data"), nil },
			}
			bridge, err := NewRemoteBridge(row.Recipe, deps)
			if err != nil {
				t.Fatal(err)
			}
			check, err := bridge.Check(context.Background())
			switch kind {
			case "tools":
				if err != nil || !check.Authenticated || check.ToolCount != 1 {
					t.Fatalf("actual MCP discovery %+v %v", check, err)
				}
			case "zero-tools":
				if err == nil {
					t.Fatal("empty MCP tool set reported ready")
				}
			case "401":
				if err != nil || check.Authenticated {
					t.Fatalf("401 invented authorization %+v %v", check, err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			for _, method := range methods {
				if method == "tools/call" {
					t.Fatal("connection check invoked a business tool")
				}
			}
		})
	}
}
func TestRemoteProbeCancellationDisconnectsAndNeverReturnsReady(t *testing.T) {
	_, row := remoteFixture(t, "none")
	release := make(chan struct{})
	disconnected := make(chan struct{}, 4)
	deps := RemoteDependencies{Probe: func(context.Context, string, protocol.JSONObject) (RemoteProbe, error) {
		<-release
		return RemoteProbe{"connected", 4}, nil
	}, OAuth: func(context.Context, string, protocol.JSONObject, func(string) error) error { return nil }, Disconnect: func(context.Context, string, protocol.JSONObject) error { disconnected <- struct{}{}; return nil }, LoadOptions: func(context.Context, string) (protocol.JSONObject, error) { return protocol.NewJSONObject(), nil }, SaveOptions: func(context.Context, string, protocol.JSONObject, protocol.JSONObject) error { return nil }, DeleteOptions: func(context.Context, string) error { return nil }, ClearOAuth: func(context.Context, string, protocol.JSONObject) error { return nil }, Environment: func(string) (string, bool) { return "", false }, PluginDataDirectory: func(context.Context, string) (string, error) { return "/owned/data", nil }}
	bridge, err := NewRemoteBridge(row.Recipe, deps)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	check, err := bridge.Check(ctx)
	if err == nil || check.Authenticated {
		t.Fatalf("cancellation incorrectly completed %+v %v", check, err)
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case <-disconnected:
		case <-time.After(time.Second):
			t.Fatal("aborted native probe did not disconnect its owned cache")
		}
	}
}
