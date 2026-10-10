package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/autoquestion"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerproxytransport"
)

func sendDesktopAutoQuestion(ctx context.Context, target *autoquestion.Target, data json.RawMessage, responsesModel string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header = target.Headers.Clone()
	for name, value := range map[string]string{"Accept": "*/*", "Accept-Encoding": "gzip, deflate, br, zstd", "User-Agent": "Bun/1.3.14", "Connection": "keep-alive"} {
		if req.Header.Get(name) == "" {
			req.Header.Set(name, value)
		}
	}
	response, err := target.Client.Do(req)
	if err != nil {
		return nil, err
	}
	decoded, err := providerproxytransport.DecodeFetchBody(response.Body, response.Header.Get("Content-Encoding"))
	if err != nil {
		_ = response.Body.Close()
		return nil, err
	}
	response.Body = decoded
	if responsesModel == "" || response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, nil
	}
	defer response.Body.Close()
	result, err := providerproxy.CollectResponses(ctx, response.Body, responsesModel, providerproxy.ResponsesReaderOptions{})
	if err != nil {
		return nil, err
	}
	data = raw(result)
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.Header = http.Header{"Content-Type": {"application/json"}}
	response.ContentLength = int64(len(data))
	return response, nil
}
