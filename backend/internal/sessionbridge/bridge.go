package sessionbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

type Config struct{ Endpoint, Token, SessionID string }

func GetConfig(environment map[string]string) (*Config, error) {
	endpoint, token, id := environment["CC_HAHA_DESKTOP_SERVER_URL"], environment["CC_HAHA_SESSION_COLLABORATION_TOKEN"], environment["CC_HAHA_SESSION_ID"]
	if endpoint == "" || token == "" || id == "" {
		return nil, nil
	}
	u, e := weburl.Parse(endpoint)
	if e != nil {
		return nil, fmt.Errorf("%s cannot be parsed as a URL.", protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(endpoint))))
	}
	if u.Protocol() != "http:" || u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "[::1]" || u.Username() != "" || u.Password() != "" {
		return nil, errors.New("Session collaboration requires a loopback desktop server")
	}
	return &Config{Endpoint: "http://" + u.Host(), Token: token, SessionID: id}, nil
}

func (c *Config) Call(ctx context.Context, action string, input protocol.JSONObject) (protocol.JSONObject, error) {
	if c == nil {
		return protocol.JSONObject{}, errors.New("Session collaboration is only available in desktop sessions")
	}
	ctx, cancel := context.WithTimeout(ctx, 305*time.Second)
	defer cancel()
	data, e := input.MarshalJSON()
	if e != nil {
		return protocol.JSONObject{}, e
	}
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+"/api/session-collaboration/"+action, bytes.NewReader(data))
	if e != nil {
		return protocol.JSONObject{}, e
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("X-Session-Id", c.SessionID)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Unexpected redirect") }}
	response, e := client.Do(request)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	defer response.Body.Close()
	data, e = io.ReadAll(response.Body)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	var result protocol.JSONObject
	parsed := result.UnmarshalJSON(data) == nil && result.IsObject()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail := ""
		if parsed {
			switch result.String("error") {
			case "BAD_REQUEST", "NOT_FOUND", "CONFLICT", "WAIT_INTERRUPTED":
				if value := result.Get("message"); value != nil && value[0] == '"' {
					units, _ := protocol.DecodeJavaScriptJSONString(value)
					if len(units) > 1000 {
						units = units[:1000]
					}
					var text string
					_ = json.Unmarshal(protocol.QuoteJavaScriptUTF16(units), &text)
					detail = ": " + text
				}
			}
		}
		return protocol.JSONObject{}, fmt.Errorf("Session collaboration %s failed (%d)%s", action, response.StatusCode, detail)
	}
	if !parsed {
		return protocol.JSONObject{}, errors.New("Invalid session collaboration response")
	}
	return result, nil
}
