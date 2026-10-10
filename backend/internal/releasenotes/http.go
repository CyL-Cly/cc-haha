package releasenotes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

// HTTPFetcher uses the supplied owned Axios-compatible HTTP client; it never
// reads ambient provider credentials or the process proxy environment.
func HTTPFetcher(client *http.Client, url string) func(context.Context) (FetchResponse, error) {
	return func(ctx context.Context) (FetchResponse, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return FetchResponse{}, err
		}
		request.Header.Set("Accept", "application/json, text/plain, */*")
		request.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
		response, err := client.Do(request)
		if err != nil {
			return FetchResponse{}, err
		}
		defer response.Body.Close()
		body, err := webfetch.ReadDecodedResponse(response)
		if err != nil {
			return FetchResponse{}, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return FetchResponse{}, fmt.Errorf("changelog HTTP status %d", response.StatusCode)
		}
		body = protocol.DecodeJavaScriptUTF8(body)
		body = []byte(strings.TrimPrefix(string(body), "\ufeff"))
		value := FetchResponse{Status: response.StatusCode, Content: string(body), StringContent: true}
		// Axios's default forcedJSONParsing accepts JSON regardless of content-type;
		// an object/number cannot be passed to Node writeFile as string data.
		if json.Valid(body) {
			units, e := protocol.DecodeJavaScriptJSONString(body)
			if e != nil {
				value.StringContent = false
			} else {
				value.Content = string(utf16.Decode(units))
			}
		}
		return value, nil
	}
}
