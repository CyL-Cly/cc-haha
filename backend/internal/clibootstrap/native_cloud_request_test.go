package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
)

func cloudFixtureEvent(data string) []byte {
	headers := []byte{}
	for _, row := range [][2]string{{":message-type", "event"}, {":event-type", "chunk"}} {
		headers = append(headers, byte(len(row[0])))
		headers = append(headers, row[0]...)
		headers = append(headers, 7, byte(len(row[1])>>8), byte(len(row[1])))
		headers = append(headers, row[1]...)
	}
	body, _ := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString([]byte(data))})
	length := 16 + len(headers) + len(body)
	raw := make([]byte, length)
	binary.BigEndian.PutUint32(raw, uint32(length))
	binary.BigEndian.PutUint32(raw[4:], uint32(len(headers)))
	binary.BigEndian.PutUint32(raw[8:], crc32.ChecksumIEEE(raw[:8]))
	copy(raw[12:], headers)
	copy(raw[12+len(headers):], body)
	binary.BigEndian.PutUint32(raw[length-4:], crc32.ChecksumIEEE(raw[:length-4]))
	return raw
}

func TestActualMainCloudProducerSTSRefreshExportMemoNativeSigningAndClose(t *testing.T) {
	var stsCalls, modelCalls atomic.Int32
	failures := make(chan error, 4)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/" {
			stsCalls.Add(1)
			if !strings.Contains(string(raw), "Action=GetCallerIdentity") || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
				failures <- fmt.Errorf("STS probe was not actual signed identity request")
			}
			w.Header().Set("Content-Type", "text/xml")
			w.WriteHeader(403)
			_, _ = io.WriteString(w, "<ErrorResponse><Error><Code>InvalidClientTokenId</Code><Message>private initial expired credential</Message></Error></ErrorResponse>")
			return
		}
		modelCalls.Add(1)
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=EXPORTED_PRIVATE/") || r.Header.Get("X-Amz-Security-Token") != "EXPORTED_TOKEN" {
			failures <- fmt.Errorf("actual export credentials did not sign model")
		}
		if r.URL.Path != "/model/us.anthropic.claude-sonnet-4:0/invoke-with-response-stream" {
			failures <- fmt.Errorf("wrong native model endpoint %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(cloudFixtureEvent(`{"type":"message_stop"}`))
	}))
	defer peer.Close()
	var refreshMarker, exportMarker string
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		refreshMarker = filepath.Join(n.CWD, "refresh-calls")
		exportMarker = filepath.Join(n.CWD, "export-calls")
		script := filepath.Join(n.CWD, "cloud-export.py")
		code := fmt.Sprintf("from pathlib import Path\nimport json\np=Path(%q);p.write_text(p.read_text()+'e' if p.exists() else 'e')\nprint(json.dumps({'Credentials':{'AccessKeyId':'EXPORTED_PRIVATE','SecretAccessKey':'EXPORTED_SECRET','SessionToken':'EXPORTED_TOKEN','unknown':True}}))\n", exportMarker)
		if err := os.WriteFile(script, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		settings := rawMain(map[string]any{"agentTeamsEnabled": false, "alwaysThinkingEnabled": false, "awsAuthRefresh": "printf r >> " + settingsShellQuote(refreshMarker), "awsCredentialExport": "/usr/bin/python3 " + settingsShellQuote(script), "future": map[string]bool{"preserved": true}})
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), settings, 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Read"} })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		env := protocol.NewJSONObject()
		for key, value := range map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "ANTHROPIC_BEDROCK_BASE_URL": peer.URL, "AWS_REGION": "us-east-1", "AWS_ACCESS_KEY_ID": "INITIAL_PRIVATE", "AWS_SECRET_ACCESS_KEY": "INITIAL_SECRET", "AWS_ENDPOINT_URL_STS": peer.URL, "AWS_EC2_METADATA_DISABLED": "true"} {
			env.Set(key, quoteMain(value))
		}
		if err := m.environment.Assign(env); err != nil {
			return err
		}
		resolver, err := m.nativeAWSResolver(scope)
		if err != nil {
			return err
		}
		probeCredentials, err := resolver.Resolve(ctx)
		if err != nil {
			return fmt.Errorf("actual AWS resolver: %w", err)
		}
		// Exercise the same physical boundary independently before checking the
		// memoized original wrapper, so a pre-send capability cannot masquerade
		// as an expired cloud credential in the wrapper's catch branch.
		probe := cloudauth.STS{Region: "us-east-1", Endpoint: peer.URL, Now: m.options.Native.Now, Do: func(request *http.Request) (*http.Response, error) { return m.nativeCloudHTTP(scope, request) }}
		if err = probe.CallerIdentity(ctx, probeCredentials); err == nil || !strings.Contains(err.Error(), "InvalidClientTokenId") {
			return fmt.Errorf("actual STS transport: %w", err)
		}
		for round := 0; round < 2; round++ {
			binding, err := m.nativeBedrockBinding(scope)
			if err != nil {
				return err
			}
			cloud, err := binding.Client(ctx, "us.anthropic.claude-sonnet-4:0")
			if err != nil {
				return err
			}
			if cloud.Config == nil || cloud.Config.SkipAuth {
				return fmt.Errorf("native signed cloud client missing")
			}
			driver, err := providernative.New(m.Owner, scope, nil)
			if err != nil {
				return err
			}
			bodyIO, err := provider.NewOwnerBodyIO(driver, func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
				return scope.AwaitBytes(cooperative.StreamNext, work)
			})
			if err != nil {
				return err
			}
			client := provider.NewAnthropic(provider.AnthropicConfig{BaseURL: cloud.BaseURL, Timeout: 10 * time.Second, Bedrock: cloud.Config, HTTPExchange: &provider.RawHTTP1Exchange{}, OwnerNativeIO: driver, OwnerBodyIO: bodyIO})
			stream, err := client.OpenMessagesStream(ctx, &provider.MessagesRequest{Path: "/v1/messages?beta=true", Body: []byte(`{"model":"us.anthropic.claude-sonnet-4:0","stream":true,"messages":[]}`)})
			if err != nil {
				return err
			}
			owned, err := provider.NewOwnedMessageStream(scope, m.Owner, stream)
			if err != nil {
				return err
			}
			pending, err := owned.Iterator.Next(scope)
			if err != nil {
				return err
			}
			result, err := pending.Await(scope)
			if err != nil || result.Done {
				return fmt.Errorf("cloud result %v", err)
			}
			if err = owned.Close(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stsCalls.Load() != 3 || modelCalls.Load() != 2 {
		t.Fatal("memo changed actual probe/model count", stsCalls.Load(), modelCalls.Load())
	}
	for path, want := range map[string]string{refreshMarker: "r", exportMarker: "e"} {
		raw, err := os.ReadFile(path)
		if err != nil || string(raw) != want {
			t.Fatal("actual auth command count", path, string(raw), err)
		}
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	// Retained unknown settings and real cache remain on the same MainPrint.
	if _, cached := m.awsCredentials.Cached(); !cached {
		t.Fatal("original AWS cache not settled")
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	// The cache's native producer must no longer admit work after shutdown.
	if _, err = m.awsCredentials.Begin(func(context.Context) (json.RawMessage, error) { return nil, nil }); err == nil {
		t.Fatal("closed actual cloud cache admitted producer")
	}
}

func TestActualSharedMainBedrockASKWriteReadBinaryRequestsAndTranscript(t *testing.T) {
	var calls atomic.Int32
	var target string
	failures := make(chan error, 8)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round := int(calls.Add(1))
		raw, _ := io.ReadAll(r.Body)
		var body protocol.JSONObject
		if err := body.UnmarshalJSON(raw); err != nil {
			failures <- err
			return
		}
		if body.Get("model") != nil || body.Get("stream") != nil || body.String("anthropic_version") != "bedrock-2023-05-31" || !strings.Contains(r.Header.Get("Authorization"), "Credential=BEDROCK_PRIVATE/") {
			failures <- fmt.Errorf("unadapted shared Harness request")
			return
		}
		var betas []string
		_ = json.Unmarshal(body.Get("anthropic_beta"), &betas)
		if len(betas) == 0 {
			failures <- fmt.Errorf("original Bedrock beta body missing")
			return
		}
		recorder := httptest.NewRecorder()
		tool := ""
		input := map[string]any{}
		if round == 1 {
			tool = "Write"
			input = map[string]any{"file_path": target, "content": "CLOUD_WRITE_SAME_HARNESS"}
		} else if round == 2 {
			tool = "Read"
			input = map[string]any{"file_path": target}
		}
		emitPlanFixture(recorder, round, tool, "CLOUD_WRITE_READ_DONE")
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
		target = filepath.Join(n.CWD, "bedrock-actual-main.txt")
		n.Environment["CLAUDE_CODE_USE_BEDROCK"] = "1"
		n.Environment["ANTHROPIC_BEDROCK_BASE_URL"] = peer.URL
		n.Environment["AWS_REGION"] = "us-east-1"
		n.Environment["AWS_ACCESS_KEY_ID"] = "BEDROCK_PRIVATE"
		n.Environment["AWS_SECRET_ACCESS_KEY"] = "BEDROCK_SECRET"
		n.Environment["AWS_EC2_METADATA_DISABLED"] = "true"
	}, func(o *MainPrintOptions) {
		o.Arguments.Model = "us.anthropic.claude-sonnet-4-20250514-v1:0"
		o.Arguments.Tools = []string{"Write,Read"}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]any{"role": "user", "content": "Write the marker and read it with the actual native file tools."})})
	if err := m.RunTurn(ctx, user); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	if calls.Load() != 3 {
		t.Fatal("unexpected original shared Harness model rounds", calls.Load())
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "CLOUD_WRITE_SAME_HARNESS" {
		t.Fatal("native file tools did not commit", string(raw), err)
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", m.Controller.SessionID()+".jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatal("original transcript path missing", matches, err)
	}
	transcript, err := os.ReadFile(matches[0])
	if err != nil || !bytes.Contains(transcript, []byte("CLOUD_WRITE_SAME_HARNESS")) {
		t.Fatal("real canonical tool result missing", err)
	}
}
