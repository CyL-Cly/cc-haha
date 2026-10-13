package computerusemcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpcli"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type StdioOptions struct {
	Service  *computeruse.Service
	Platform string
	Input    io.Reader
	Output   io.Writer
}
type unboundHost struct {
	service  *computeruse.Service
	platform string
}

func (h unboundHost) Catalog(ctx context.Context) ([]protocol.JSONObject, error) {
	if h.service.Disabled(ctx) {
		return []protocol.JSONObject{}, nil
	}
	if h.platform == "windows" || h.platform == "win32" {
		return WindowsDefinitions("pixels")
	}
	var tools []protocol.JSONObject
	err := json.Unmarshal(toolDefinitions, &tools)
	return tools, err
}
func (h unboundHost) Call(context.Context, string, json.RawMessage) (protocol.JSONObject, error) {
	// The Source stdio server intentionally has no session context. Ordinary
	// Agent calls use the SID-bound in-process Binder instead of this stub.
	var result protocol.JSONObject
	err := result.UnmarshalJSON([]byte(`{"content":[{"type":"text","text":"This computer-use server instance is not wired to a session. Per-session app permissions are not available on this code path."}],"isError":true}`))
	return result, err
}
func (h unboundHost) Close(ctx context.Context) error { return h.service.Close(ctx) }
func RunStdio(ctx context.Context, options StdioOptions) error {
	if options.Service == nil || options.Platform != "darwin" && options.Platform != "windows" && options.Platform != "win32" {
		return errors.New("Computer Use MCP is supported on macOS and Windows")
	}
	return mcpcli.Serve(ctx, mcpcli.ServeOptions{Version: "0.1.3", ServerName: "computer-use", Capabilities: json.RawMessage(`{"tools":{},"logging":{}}`), Input: options.Input, Output: options.Output, Host: unboundHost{options.Service, options.Platform}})
}
