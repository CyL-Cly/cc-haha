package desktopoauth

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net/http"
)

// CLIClientTransport borrows the existing original-client request producer,
// without constructing a desktop account, storage, listener or session registry.
type CLIClientTransport struct {
	HTTP        *http.Client
	Environment func() map[string]string
	Settings    func(context.Context) (protocol.JSONObject, error)
}

func ExecuteCLIOpenAITokenRequest(ctx context.Context, o CLIClientTransport, pairs [][2]string) (protocol.JSONObject, error) {
	service := &Service{options: Options{HTTPClient: o.HTTP, Environment: o.Environment, Settings: o.Settings}}
	return service.desktopTokenRequest(ctx, "exchange", "OpenAI", OpenAIAuthIssuer+"/oauth/token", OpenAICodexTokenUserAgent, pairs)
}
