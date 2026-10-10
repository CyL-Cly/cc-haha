//go:build darwin

package provider

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io"
	"net/http"
	"testing"
	"time"
)

// Original caller/server inputs, byte copied from the frozen Source phased
// probe; no captured headers, prepared requests or Source decisions are input.
//
//go:embed testdata/native_poll_original.json
var nativePollOriginal []byte

func TestNativePollOriginalSourcePhasesPhysicalFacts(t *testing.T) {
	var original struct {
		APIKey   string          `json:"apiKey"`
		Body     json.RawMessage `json:"body"`
		Response json.RawMessage `json:"response"`
		Profiles []struct {
			Name         string `json:"name"`
			Timeout      int    `json:"timeout"`
			ReleaseDelay int    `json:"releaseDelay"`
			Headers      string `json:"headers"`
			BodyRelease  string `json:"bodyRelease"`
			CallerAbort  bool   `json:"callerAbortDuringHold"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(nativePollOriginal, &original); err != nil {
		t.Fatal(err)
	}
	originalBody, err := protocol.ParseAndStringifyJavaScriptJSON(original.Body)
	if err != nil {
		t.Fatal(err)
	}
	responseBody, err := protocol.ParseAndStringifyJavaScriptJSON(original.Response)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range original.Profiles {
		t.Run(profile.Name, func(t *testing.T) {
			connection, peer := nativePair(t)
			request, _ := http.NewRequest("POST", "http://fixture.invalid/v1/messages", bytes.NewReader(originalBody))
			request.Header.Set("x-api-key", original.APIKey)
			request.Header.Set("Content-Type", "application/json")
			sent := make(chan error, 1)
			go func() { sent <- request.Write(connection) }()
			captured, err := http.ReadRequest(bufio.NewReader(peer))
			if err != nil {
				t.Fatal(err)
			}
			capturedBody, err := io.ReadAll(captured.Body)
			if err != nil {
				t.Fatal(err)
			}
			if err = <-sent; err != nil || !bytes.Equal(capturedBody, originalBody) || captured.Header.Get("x-api-key") != original.APIKey {
				t.Fatalf("original request %q %v", capturedBody, err)
			}
			operation, err := NewNativeHTTP1Operation(connection, 65536)
			if err != nil {
				t.Fatal(err)
			}
			defer operation.Close()
			physicalRelease := make(chan error, 1)
			// This delay is the original private SERVER schedule, not checkpoint sleep,
			// timeout padding or a worker scheduling guess. The write/cue is the barrier.
			timer := time.AfterFunc(time.Duration(profile.ReleaseDelay)*time.Millisecond, func() {
				var headers string
				if profile.Headers == "complete" {
					headers = fmt.Sprintf("HTTP/1.1 200 OK\r\ncontent-type: application/json\r\ncontent-length: %d\r\nrequest-id: owner-phase-request\r\nconnection: keep-alive\r\n\r\n", len(responseBody))
				} else if profile.Headers == "partial" {
					headers = "HTTP/1.1 200 OK\r\ncontent-type: application/json\r\n"
				}
				_, err := io.WriteString(peer, headers)
				physicalRelease <- err
			})
			defer timer.Stop()
			if err = <-physicalRelease; err != nil {
				t.Fatal(err)
			}
			if profile.Headers != "none" {
				nativeReady(t, operation)
			}
			checkpoint := operation.Checkpoint()
			responseBodyRaw := []byte(nil)
			switch profile.Headers {
			case "complete":
				if checkpoint.State != NativeHTTP1HeaderComplete || checkpoint.Err != nil {
					t.Fatal(checkpoint)
				}
				// Owner HTTP adoption is independent of Source response observation. Body
				// is still held, and does not have to finish for headers to be adopted.
				response := nativeAdopt(t, operation)
				if response.Header.Get("request-id") != "owner-phase-request" {
					t.Fatal(response.Header)
				}
				nativeWrite(t, peer, string(responseBody))
				body, err := io.ReadAll(response.Body)
				responseBodyRaw = body
				if err != nil || !bytes.Equal(body, responseBody) {
					t.Fatalf("original body %q %v", body, err)
				}
			case "partial":
				if checkpoint.State != NativeHTTP1Partial || checkpoint.Err != nil {
					t.Fatal(checkpoint)
				}
			case "none":
				if checkpoint.State != NativeHTTP1NoReadable || checkpoint.Received != 0 || checkpoint.Err != nil {
					t.Fatal(checkpoint)
				}
			default:
				t.Fatal("unimplemented original header schedule")
			}
			// Synchronous original caller abort is an explicit lease close, not a native
			// worker timer. This probe makes no APIUserAbortError/SDK timer policy claim.
			if err = operation.Close(); err != nil {
				t.Fatal(err)
			}
			var last [1]byte
			count, err := peer.Read(last[:])
			if count != 0 || err != io.EOF {
				t.Fatalf("original peer close %d %v", count, err)
			}
			t.Logf("NATIVE_PHASE %s", mustNativeJSON(t, map[string]any{"profile": profile.Name, "state": checkpoint.State, "received": checkpoint.Received, "header": string(checkpoint.Header), "prefixBase64": base64.StdEncoding.EncodeToString(operation.bytes), "requestCount": 1, "peerEOF": true, "callerAbortDeclared": profile.CallerAbort, "sdkTimerImplemented": false, "bodyBase64": base64.StdEncoding.EncodeToString(responseBodyRaw), "requestBodyBase64": base64.StdEncoding.EncodeToString(capturedBody), "requestPath": captured.RequestURI}))
		})
	}
}
func mustNativeJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
