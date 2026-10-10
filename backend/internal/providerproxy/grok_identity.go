package providerproxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const GrokCLIBaseURL = "https://cli-chat-proxy.grok.com/v1"
const GrokCLIVersion = "1.0.46"

func BuildGrokUserAgent(version, platform, arch string) string {
	if platform == "darwin" {
		platform = "macos"
	}
	if platform == "win32" || platform == "windows" {
		platform = "windows"
	}
	switch arch {
	case "x64", "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	case "ia32", "386":
		arch = "x86"
	}
	return fmt.Sprintf("grok-pager/%s grok-shell/%s (%s; %s)", version, version, platform, arch)
}
func BuildGrokIdentityHeaders(access json.RawMessage, platform, arch string) (http.Header, error) {
	raw, err := protocol.ExactJavaScriptStringJSON(access)
	if err != nil {
		return nil, err
	}
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil, err
	}
	for i, unit := range units {
		if unit > 255 {
			return nil, fmt.Errorf("Cannot convert argument to a ByteString because the character at index %d has a value of %d which is greater than 255", i+7, unit)
		}
	}
	var value string
	_ = json.Unmarshal(raw, &value)
	value = strings.Trim("Bearer "+value, "\t \r\n")
	if strings.ContainsAny(value, "\x00\r\n") {
		return nil, fmt.Errorf("Headers.set: invalid header value")
	}
	headers := http.Header{}
	for key, value := range map[string]string{"Accept": "application/json, text/event-stream", "Authorization": value, "Content-Type": "application/json", "X-XAI-Token-Auth": "xai-grok-cli", "x-grok-client-version": GrokCLIVersion, "x-grok-client-mode": "interactive", "x-grok-client-identifier": "grok-pager", "x-authenticateresponse": "authenticate-response", "User-Agent": BuildGrokUserAgent(GrokCLIVersion, platform, arch)} {
		headers.Set(key, value)
	}
	return headers, nil
}
