package mcpclient

import "context"

// stubs/ant-claude-for-chrome-mcp.ts connect() returns without registering any
// JSON-RPC handlers. Its linked transport accepts initialize but never replies.
type chromeStubTransport struct{}

func (*chromeStubTransport) start(context.Context, func(rpcMessage), func(error), func()) error {
	return nil
}
func (*chromeStubTransport) send(context.Context, rpcMessage) error { return nil }
func (*chromeStubTransport) setProtocolVersion(string)              {}
func (*chromeStubTransport) close() error                           { return nil }
