//go:build darwin

package provider_test

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
)

func privateBedrockFrame(data string) []byte {
	headers := []byte{}
	for _, row := range [][2]string{{":message-type", "event"}, {":event-type", "chunk"}, {":content-type", "application/json"}} {
		headers = append(headers, byte(len(row[0])))
		headers = append(headers, row[0]...)
		headers = append(headers, 7, byte(len(row[1])>>8), byte(len(row[1])))
		headers = append(headers, row[1]...)
	}
	body, _ := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(data))})
	length := 16 + len(headers) + len(body)
	message := make([]byte, length)
	binary.BigEndian.PutUint32(message, uint32(length))
	binary.BigEndian.PutUint32(message[4:], uint32(len(headers)))
	binary.BigEndian.PutUint32(message[8:], crc32.ChecksumIEEE(message[:8]))
	copy(message[12:], headers)
	copy(message[12+len(headers):], body)
	binary.BigEndian.PutUint32(message[length-4:], crc32.ChecksumIEEE(message[:length-4]))
	return message
}

func TestBedrockActualNativeOwnerSignedBytesBinaryDemandReturnAndClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stamp := time.Date(2026, 10, 8, 1, 2, 3, 0, time.UTC)
	token := "FAKE_PRIVATE_TOKEN"
	credentials := cloudauth.AWSCredentials{AccessKeyID: "FAKE_PRIVATE_ACCESS", SecretAccessKey: "FAKE_PRIVATE_SECRET", SessionToken: &token}
	var requests atomic.Int32
	returned := make(chan struct{})
	serverErrors := make(chan error, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			serverErrors <- err
			return
		}
		var body map[string]json.RawMessage
		if err = json.Unmarshal(raw, &body); err != nil {
			serverErrors <- err
			return
		}
		if body["model"] != nil || body["stream"] != nil || string(body["anthropic_version"]) != `"bedrock-2023-05-31"` {
			serverErrors <- fmt.Errorf("incorrect actual Bedrock body %s", raw)
			return
		}
		headers := map[string]string{}
		_, signedNames, _ := strings.Cut(r.Header.Get("Authorization"), "SignedHeaders=")
		signedNames, _, _ = strings.Cut(signedNames, ", Signature=")
		for _, key := range strings.Split(signedNames, ";") {
			if key == "host" {
				headers[key] = r.Host
			} else {
				headers[key] = r.Header.Get(key)
			}
		}
		signed := cloudauth.Sign(cloudauth.SigningRequest{Method: r.Method, Path: r.URL.EscapedPath(), Headers: headers, Body: raw}, credentials, cloudauth.SignOptions{Region: "us-east-1", Service: "bedrock", Time: stamp})
		if signed.Headers["authorization"] != r.Header.Get("Authorization") {
			serverErrors <- errors.New("native physical bytes/header signature changed")
			return
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.Header().Set("x-future-bedrock", "original")
		w.WriteHeader(200)
		frame := privateBedrockFrame(`{"type":"message_start","message":{"id":"bedrock-private","content":[],"role":"assistant","type":"message","usage":{"input_tokens":1,"output_tokens":0}}}`)
		// Every physical chunk boundary differs from the logical event boundary.
		for start := 0; start < len(frame); start += 7 {
			end := min(start+7, len(frame))
			_, _ = w.Write(frame[start:end])
			w.(http.Flusher).Flush()
		}
		if strings.Contains(r.URL.Path, "abort") {
			<-r.Context().Done()
			close(returned)
			return
		}
		_, _ = w.Write(privateBedrockFrame(`{"type":"message_stop"}`))
	}))
	defer server.Close()
	for _, model := range []string{"private:model", "abort:model"} {
		owner := cooperative.New()
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
			client := provider.NewAnthropic(provider.AnthropicConfig{BaseURL: server.URL, Timeout: 10 * time.Second, HTTPExchange: &provider.RawHTTP1Exchange{}, OwnerNativeIO: driver, OwnerBodyIO: bodyIO, Bedrock: &provider.BedrockConfig{Region: "us-east-1", Now: func() time.Time { return stamp }, Credentials: func(context.Context) (cloudauth.AWSCredentials, error) {
				if !scope.BelongsTo(owner) {
					return cloudauth.AWSCredentials{}, cooperative.ErrRestricted
				}
				return credentials, nil
			}}})
			rawBody, _ := json.Marshal(map[string]any{"model": model, "stream": true, "messages": []any{}})
			raw, err := client.OpenMessagesStream(ctx, &provider.MessagesRequest{Path: "/v1/messages?beta=true", Body: rawBody})
			if err != nil {
				return err
			}
			stream, err := provider.NewOwnedMessageStream(scope, owner, raw)
			if err != nil {
				return err
			}
			first, err := stream.Iterator.Next(scope)
			if err != nil {
				return err
			}
			value, err := first.Await(scope)
			if err != nil {
				return err
			}
			if value.Done || value.Value.Object == nil {
				select {
				case failure := <-serverErrors:
					return failure
				default:
				}
				return errors.New("missing actual Bedrock first event")
			}
			if strings.HasPrefix(model, "abort") {
				stop, err := stream.Iterator.ReturnUndefined(scope)
				if err != nil {
					return err
				}
				if _, err = stop.Await(scope); err != nil {
					return err
				}
				return stream.Close()
			}
			next, err := stream.Iterator.Next(scope)
			if err != nil {
				return err
			}
			value, err = next.Await(scope)
			if err != nil || value.Done {
				return fmt.Errorf("missing final event: %v", err)
			}
			next, err = stream.Iterator.Next(scope)
			if err != nil {
				return err
			}
			value, err = next.Await(scope)
			if err != nil || !value.Done {
				return fmt.Errorf("missing binary EOF: %v", err)
			}
			return stream.Close()
		})
		owner.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-returned:
	case <-ctx.Done():
		t.Fatal("native stream Return did not close actual socket")
	}
	select {
	case err := <-serverErrors:
		t.Fatal(err)
	default:
	}
	if requests.Load() != 2 {
		t.Fatal("unexpected physical send count", requests.Load())
	}
	// Unknown chunk events remain ignored by the ordinary SSE parser.
	plain := provider.NewAnthropic(provider.AnthropicConfig{APIKey: "private", Timeout: 10 * time.Second, HTTPClient: &http.Client{Transport: bedrockSSEFixture{}}})
	raw, err := plain.OpenMessagesStream(ctx, &provider.MessagesRequest{Path: "/v1/messages", Body: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err = raw.Next(); err != io.EOF {
		t.Fatal("ordinary SSE chunk event consumed", err)
	}
}

type bedrockSSEFixture struct{}

func (bedrockSSEFixture) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("event: chunk\ndata: not-json\n\n")), Request: r}, nil
}
