package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestPublicCLIMCPPromptListGetMediaAndCanonicalModel(t *testing.T) {
	var lists, gets, models atomic.Int32
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var message protocol.JSONObject
		_ = message.UnmarshalJSON(data)
		if message.Get("id") == nil {
			w.WriteHeader(202)
			return
		}
		var result json.RawMessage
		switch message.String("method") {
		case "initialize":
			result = []byte(`{"protocolVersion":"2025-06-18","capabilities":{"prompts":{}},"serverInfo":{"name":"public-cli-prompt","version":"1"}}`)
		case "prompts/list":
			lists.Add(1)
			result = []byte(`{"prompts":[{"name":"review.file","description":"Actual private prompt","arguments":[{"name":"file","required":true},{"name":"mode"}]}],"nextCursor":"do-not-follow"}`)
		case "prompts/get":
			gets.Add(1)
			params, _ := message.Object("params")
			arguments, _ := params.Object("arguments")
			if params.String("name") != "review.file" || arguments.String("file") != "owned-file" || arguments.String("mode") != "" {
				t.Error("public prompt args/name differ from original literal-space mapping", params)
			}
			result = []byte(`{"messages":[{"role":"assistant","content":{"type":"text","text":"Actual public prompt media body"}},{"role":"user","content":{"type":"audio","data":"AAE/Cg==","mimeType":"audio/wav"}}]}`)
		default:
			t.Error("public prompt reached unrelated MCP request", message)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(protocol.NewJSONObject(protocol.JSONField{Name: "jsonrpc", Value: raw("2.0")}, protocol.JSONField{Name: "id", Value: message.Get("id")}, protocol.JSONField{Name: "result", Value: result}))
	}))
	defer mcp.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		data, _ := io.ReadAll(r.Body)
		models.Add(1)
		if !bytes.Contains(data, []byte("Actual public prompt media body")) || !bytes.Contains(data, []byte("[Audio from owned-peer]")) || !bytes.Contains(data, []byte("mcp__owned-peer__review.file")) || bytes.Contains(data, []byte("AAE/Cg==")) {
			t.Error("public prompt failed actual model/media consumer", string(data))
		}
		response(w, "public-prompt", "", nil, "Actual prompt completed")
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("isolated fixture Keychain unavailable")
	}
	config, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"owned-peer": map[string]string{"type": "http", "url": mcp.URL}}})
	command, _, err := ParseCommand([]string{"-p", "--mcp-config", string(config), "--model", "fixture-model", "--tools", "", "--thinking", "disabled", "--max-turns", "1", "--system-prompt", "Actual public MCP prompt consumer", "/owned-peer:review.file (MCP) owned-file  extra"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var output bytes.Buffer
	code, err := RunCommand(ctx, command, o, strings.NewReader(""), &output, nil)
	if err != nil || code != 0 || !strings.Contains(output.String(), "Actual prompt completed") || lists.Load() != 1 || gets.Load() != 1 || models.Load() != 1 {
		t.Fatal("public CLI prompt command did not settle through actual Harness", code, err, output.String(), lists.Load(), gets.Load(), models.Load())
	}
	files := 0
	if err := filepath.WalkDir(o.Native.Home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.Contains(entry.Name(), "mcp-owned-peer-blob-") {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.Equal(data, []byte{0, 1, 63, 10}) {
				t.Error("public prompt media persisted incorrect actual bytes", path)
			}
			files++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if files != 1 {
		t.Fatal("actual prompt audio did not reach physical session tool-results", files)
	}
}
