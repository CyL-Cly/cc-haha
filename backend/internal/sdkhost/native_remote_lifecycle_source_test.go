package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
)

func TestActualSourceRemoteInlineAndOrphanResponseDeliveryLifecycle(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	allocation, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(allocation, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	messages := []protocol.JSONObject{
		object([]byte(`{"type":"keep_alive","uuid":"no-keepalive-ack"}`)),
		object([]byte(`{"type":"control_request","uuid":"mode-delivery","request_id":"mode","request":{"subtype":"set_permission_mode","mode":"default"}}`)),
		object([]byte(`{"type":"control_response","uuid":"orphan-delivery","response":{"subtype":"success","request_id":"orphan","response":{}}}`)),
		object([]byte(`{"type":"control_response","uuid":"orphan-delivery","response":{"subtype":"success","request_id":"orphan","response":{}}}`)),
		object([]byte(`{"type":"control_response","uuid":"error-delivery","response":{"subtype":"error","request_id":"late","error":"old request"}}`)),
		object([]byte(`{"type":"control_response","uuid":42,"response":{"subtype":"success","request_id":"nonstring","response":{}}}`)),
		object([]byte(`{"type":"user","uuid":"user-later","message":{"role":"user","content":"not yet ASK"}}`)),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../testdata/native-remoteio-lifecycle.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_REMOTEIO_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(raw(map[string]any{"messages": messages}))
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(data))
	}
	var source struct {
		Yielded      []protocol.JSONObject
		Lifecycle    []struct{ ID, State string }
		Capabilities struct{ Blocked []string }
	}
	if err := json.Unmarshal(data, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(data))
	}
	var mu sync.Mutex
	deliveries := []struct{ ID, State string }{}
	completed := make(chan struct{})
	var once sync.Once
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if r.Method == http.MethodGet {
			io.WriteString(w, `{"worker":{"external_metadata":{}}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/delivery") {
			var body struct {
				Updates []struct {
					EventID string `json:"event_id"`
					Status  string `json:"status"`
				}
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			mu.Lock()
			for _, row := range body.Updates {
				deliveries = append(deliveries, struct{ ID, State string }{row.EventID, row.Status})
			}
			if len(deliveries) == len(source.Lifecycle) {
				once.Do(func() { close(completed) })
			}
			mu.Unlock()
		}
		io.WriteString(w, `{}`)
	}))
	defer peer.Close()
	remote, err := remoteio.New(ctx, remoteio.Options{URL: peer.URL + "/sessions/fixture", Environment: map[string]string{"CLAUDE_CODE_USE_CCR_V2": "1", "CLAUDE_CODE_SESSION_ACCESS_TOKEN": "fake-delivery", "CLAUDE_CODE_WORKER_EPOCH": "3"}})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	remote.Start()
	if _, err = remote.RestoredWorkerState(ctx); err != nil {
		t.Fatal(err)
	}
	owner := cooperative.New()
	defer owner.Close()
	yielded := []protocol.JSONObject{}
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		chunks := []string{}
		for _, message := range messages {
			chunks = append(chunks, string(raw(message))+"\n")
		}
		iterator, err := chunkIterator(owner, chunks)
		if err != nil {
			return err
		}
		input, err := NewPlainStructuredInput(owner, iterator)
		if err != nil {
			return err
		}
		if err = input.BindRemoteIO(scope, remote); err != nil {
			return err
		}
		input.allowSDKPermissionMode = true
		input.sandboxResponse = func(*cooperative.Scope, protocol.JSONObject) (bool, error) { return true, nil }
		for {
			next, err := input.Next(scope)
			if err != nil {
				return err
			}
			result, err := next.Await(scope)
			if err != nil {
				return err
			}
			if result.Done {
				break
			}
			input.completeRemoteInlineLifecycle(result.Value)
			yielded = append(yielded, result.Value)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-completed:
	case <-ctx.Done():
		t.Fatal("inline/orphan delivery ACK never reached actual HTTP consumer")
	}
	mu.Lock()
	defer mu.Unlock()
	want := []struct{ ID, State string }{}
	for _, row := range source.Lifecycle {
		want = append(want, struct{ ID, State string }{row.ID, map[string]string{"completed": "processed"}[row.State]})
	}
	if !reflect.DeepEqual(deliveries, want) || !bytes.Equal(raw(yielded), raw(source.Yielded)) {
		t.Fatalf("Source lifecycle %s Go %s; Source yielded %s Go %s", raw(want), raw(deliveries), raw(source.Yielded), raw(yielded))
	}
}

func TestPermissionModeInputRequiresConcretePermissionConsumer(t *testing.T) {
	for _, permission := range []bool{false, true} {
		t.Run(fmt.Sprint(permission), func(t *testing.T) {
			owner := cooperative.New()
			defer owner.Close()
			if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				rawInput, err := chunkIterator(owner, nil)
				if err != nil {
					return err
				}
				input, err := NewPlainStructuredInput(owner, rawInput)
				if err != nil {
					return err
				}
				input.allowSDKInference = true
				input.allowSDKPermissionMode = permission
				next, err := input.processLine(scope, `{"type":"control_request","request":{"subtype":"set_permission_mode","mode":"default"}}`)
				if !permission {
					if err == nil {
						return fmt.Errorf("inference-only Host admitted unavailable permission control")
					}
					return nil
				}
				if err != nil {
					return err
				}
				result, err := next.Await(scope)
				if err == nil && !result.present {
					return fmt.Errorf("concrete permission control not yielded")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
