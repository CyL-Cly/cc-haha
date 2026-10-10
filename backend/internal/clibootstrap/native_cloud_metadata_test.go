package clibootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSharedMainContainerCredentialTokenFileRetryThenNativeWriteRead(t *testing.T) {
	var credentialCalls, models atomic.Int32
	var target string
	failures := make(chan error, 10)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/private-credentials" {
			count := credentialCalls.Add(1)
			if r.Method != "GET" || r.Header.Get("Authorization") != "TOKEN_FILE_PRIVATE" {
				failures <- fmt.Errorf("HTTP credential request bypassed actual token file")
			}
			w.Header().Set("Content-Type", "application/json")
			if count < 4 {
				w.WriteHeader(500)
				_, _ = io.WriteString(w, `{"Message":"temporary private peer failure"}`)
				return
			}
			_, _ = io.WriteString(w, `{"AccessKeyId":"CONTAINER_PRIVATE","SecretAccessKey":"CONTAINER_SECRET","Token":"CONTAINER_SESSION","Expiration":"2030-01-02T03:04:05Z","AccountId":"Source modern HTTP ignores this field"}`)
			return
		}
		round := int(models.Add(1))
		raw, _ := io.ReadAll(r.Body)
		var body protocol.JSONObject
		if body.UnmarshalJSON(raw) != nil || body.Get("model") != nil || body.Get("stream") != nil || !strings.Contains(r.Header.Get("Authorization"), "Credential=CONTAINER_PRIVATE/") || r.Header.Get("X-Amz-Security-Token") != "CONTAINER_SESSION" {
			failures <- fmt.Errorf("actual model request did not use resolved container credential")
			return
		}
		tool := ""
		input := map[string]any{}
		if round == 1 {
			tool = "Write"
			input = map[string]any{"file_path": target, "content": "CONTAINER_ACTUAL_SHARED_HARNESS"}
		} else if round == 2 {
			tool = "Read"
			input = map[string]any{"file_path": target}
		}
		recorder := httptest.NewRecorder()
		emitPlanFixture(recorder, round, tool, "CONTAINER_WRITE_READ_DONE")
		wire := recorder.Body.String()
		if tool != "" {
			encoded, _ := json.Marshal(input)
			quoted, _ := json.Marshal(string(encoded))
			wire = strings.Replace(wire, `"partial_json":"{}"`, `"partial_json":`+string(quoted), 1)
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		for _, frame := range strings.Split(wire, "\n\n") {
			for _, line := range strings.Split(frame, "\n") {
				if strings.HasPrefix(line, "data: ") {
					_, _ = w.Write(cloudFixtureEvent(strings.TrimPrefix(line, "data: ")))
				}
			}
		}
	}))
	defer peer.Close()
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		delete(n.Environment, "CLAUDE_CODE_SIMPLE")
		target = filepath.Join(n.CWD, "container-write-read.txt")
		token := filepath.Join(n.Home, "private-container-token")
		if err := os.WriteFile(token, []byte("TOKEN_FILE_PRIVATE"), 0600); err != nil {
			t.Fatal(err)
		}
		for key, value := range map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "ANTHROPIC_BEDROCK_BASE_URL": peer.URL, "AWS_REGION": "us-east-1", "AWS_CONTAINER_CREDENTIALS_FULL_URI": peer.URL + "/private-credentials", "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE": token, "AWS_EC2_METADATA_DISABLED": "true"} {
			n.Environment[key] = value
		}
		delete(n.Environment, "AWS_ACCESS_KEY_ID")
		delete(n.Environment, "AWS_SECRET_ACCESS_KEY")
	}, func(o *MainPrintOptions) {
		o.Arguments.Model = "us.anthropic.claude-sonnet-4-20250514-v1:0"
		o.Arguments.Tools = []string{"Write,Read"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Write the marker and read it with the actual native file tools."})})
	if err := m.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	if credentialCalls.Load() != 6 || models.Load() != 3 {
		t.Fatal("unexpected Source HTTP retry/per-request credential sequence", credentialCalls.Load(), models.Load())
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "CONTAINER_ACTUAL_SHARED_HARNESS" {
		t.Fatal("actual native file", string(raw), err)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
