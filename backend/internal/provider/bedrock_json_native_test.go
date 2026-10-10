//go:build darwin

package provider_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

func TestBedrockActualNativeNonstreamFallbackSignedRequestBodyOwnerAndWireTrace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	seen := make(chan []byte, 3)
	failures := make(chan error, 3)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		seen <- raw
		var body protocol.JSONObject
		_ = body.UnmarshalJSON(raw)
		if r.URL.Path != "/model/private:model/invoke" || body.Get("model") != nil || body.Get("stream") != nil || body.String("anthropic_version") != "bedrock-2023-05-31" || !strings.Contains(r.Header.Get("Authorization"), "Credential=PRIVATE_ACCESS/") {
			failures <- fmt.Errorf("nonstream fallback bypassed Bedrock wrapper")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"type":"message","id":"private-json",`)
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, `"content":[],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer peer.Close()
	for _, beta := range []bool{false, true} {
		owner := cooperative.New()
		var captured []byte
		err := owner.Run(ctx, func(scope *cooperative.Scope) error {
			driver, err := providernative.New(owner, scope, nil)
			if err != nil {
				return err
			}
			bodyIO, err := provider.NewOwnerBodyIO(driver, func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
				return scope.AwaitBytes(cooperative.StreamNext, work)
			})
			if err != nil {
				return err
			}
			client := provider.NewAnthropic(provider.AnthropicConfig{BaseURL: peer.URL, Timeout: 5 * time.Second, HTTPExchange: &provider.RawHTTP1Exchange{}, OwnerNativeIO: driver, OwnerBodyIO: bodyIO, TraceBegin: func(input tracecapture.FetchInput) tracecapture.FetchCapture {
				captured = append([]byte(nil), input.PlainBody...)
				return nil
			}, Bedrock: &provider.BedrockConfig{Region: "us-east-1", Credentials: func(context.Context) (cloudauth.AWSCredentials, error) {
				return cloudauth.AWSCredentials{AccessKeyID: "PRIVATE_ACCESS", SecretAccessKey: "PRIVATE_SECRET"}, nil
			}}})
			body := protocol.NewJSONObject(protocol.JSONField{Name: "model", Value: json.RawMessage(`"private:model"`)}, protocol.JSONField{Name: "messages", Value: json.RawMessage(`[]`)}, protocol.JSONField{Name: "max_tokens", Value: json.RawMessage(`1`)})
			var result provider.MessageResponse
			if beta {
				body.Set("betas", json.RawMessage(`["private-beta"]`))
				result, err = client.CreateBetaMessage(ctx, body)
			} else {
				result, err = client.CreateMessage(ctx, body)
			}
			if err != nil {
				return err
			}
			if !bytes.Contains(result.Raw, []byte("private-json")) {
				return fmt.Errorf("native body not read on original owner")
			}
			return nil
		})
		owner.Close()
		if err != nil {
			t.Fatal(err)
		}
		wire := <-seen
		if !bytes.Equal(wire, captured) {
			t.Fatal("Trace did not capture actual adapted request bytes")
		}
		var body protocol.JSONObject
		_ = body.UnmarshalJSON(wire)
		if beta && string(body.Get("anthropic_beta")) != `["private-beta"]` {
			t.Fatal("beta resource header was not moved into wrapper body", string(wire))
		}
		select {
		case err := <-failures:
			t.Fatal(err)
		default:
		}
	}
}
