package clibootstrap

import (
	"bytes"
	"context"
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

func TestActualOrdinaryAgentQuotaResponseSharesProcessStatusLineAndParentAuth(t *testing.T) {
	childNextResponse, release := make(chan struct{}), make(chan struct{})
	var parentCalls, childCalls atomic.Int32
	var childFile string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.Header.Get("authorization") != "Bearer private-statusline-subscriber" || r.Header.Get("x-api-key") != "" {
			t.Error("actual child/parent cached subscriber HTTP", r.Header)
		}
		request := mainObj(body)
		if bytes.Contains(request.Get("system"), []byte("STATUSLINE_CHILD_RATE_MARKER")) {
			switch childCalls.Add(1) {
			case 1:
				w.Header().Set("anthropic-ratelimit-unified-5h-utilization", "0.375")
				w.Header().Set("anthropic-ratelimit-unified-5h-reset", "1700000777")
				emitNativeTaskTool(w, "child-rate-read", "Read", map[string]any{"file_path": childFile})
			case 2:
				if !bytes.Contains(body, []byte("Actual child utilization Read")) {
					t.Error("actual child Read result absent", string(body))
				}
				close(childNextResponse)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				emitPlanFixture(w, 101, "", "Child no-header response completed")
			default:
				t.Error("unexpected child HTTP", string(body))
			}
			return
		}
		switch parentCalls.Add(1) {
		case 1:
			emitNativeTaskTool(w, "statusline-agent", "Agent", map[string]any{"description": "Actual child utilization", "subagent_type": "statusline-rate-child", "prompt": "Produce the actual private child response"})
		case 2:
			emitPlanFixture(w, 2, "", "Parent no-header response completed")
		default:
			t.Error("unexpected native parent HTTP", string(body))
			http.Error(w, "unowned", 500)
		}
	}))
	defer peer.Close()
	_, parent, _ := newNativeStatusLinePhysicalFixture(t, protocol.JSONObject{}, true, func(f nativePluginFixture) {
		f.n.Environment["CLAUDE_CODE_ENTRYPOINT"] = "cli"
		rg, err := os.ReadFile("/opt/homebrew/bin/rg")
		if err != nil {
			t.Fatal(err)
		}
		rgPath := filepath.Join(f.home, "bin", "rg")
		f.write(t, rgPath, string(rg))
		if err = os.Chmod(rgPath, 0700); err != nil {
			t.Fatal(err)
		}
		f.n.Environment["CC_HAHA_RIPGREP_PATH"] = rgPath
		f.n.Environment["PATH"] = filepath.Dir(rgPath) + ":/usr/bin:/bin"
		delete(f.n.Environment, "ANTHROPIC_API_KEY")
		f.n.Environment["ANTHROPIC_BASE_URL"], f.n.Environment["NO_PROXY"] = peer.URL, "127.0.0.1,localhost"
		f.write(t, filepath.Join(f.config, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"private-statusline-subscriber","scopes":["user:inference","user:profile"],"expiresAt":9999999999999,"subscriptionType":"max"}}`)
		data, err := os.ReadFile(filepath.Join(f.config, ".claude.json"))
		if err != nil {
			t.Fatal(err)
		}
		global := mainObj(data)
		global.Set("oauthAccount", rawMain(map[string]string{"billingType": "stripe_subscription"}))
		f.write(t, filepath.Join(f.config, ".claude.json"), string(rawMain(global)))
		f.write(t, filepath.Join(f.n.CWD, ".claude", "agents", "statusline-rate-child.md"), "---\nname: statusline-rate-child\ndescription: Private quota producer\ntools: Read\nmodel: inherit\n---\nSTATUSLINE_CHILD_RATE_MARKER")
		childFile = filepath.Join(f.n.CWD, "statusline-rate-child.txt")
		f.write(t, childFile, "Actual child utilization Read")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- parent.RunTurn(ctx, protocol.NewJSONObject(fieldMain("type", "user"), fieldMain("message", protocol.NewJSONObject(fieldMain("role", "user"), fieldMain("content", "Run actual statusline-rate-child")))))
	}()
	select {
	case <-childNextResponse:
	case err := <-done:
		t.Fatal("actual Agent failed before quota response", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cache := TerminalStatusLineCache{}
	result, err := parent.RunTerminalStatusLine(ctx, "NORMAL", &cache, false)
	if err != nil || !strings.Contains(string(result.Input.Get("rate_limits")), `"used_percentage":37.5`) {
		close(release)
		t.Fatal("actual child response did not reach parent process StatusLine", err, string(rawMain(result.Input)))
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	result, err = parent.RunTerminalStatusLine(ctx, "NORMAL", &cache, false)
	if err != nil || result.Input.Get("rate_limits") != nil || parentCalls.Load() != 2 || childCalls.Load() != 2 {
		t.Fatal("parent headerless response did not clear Source process-global raw utilization", err, parentCalls.Load(), childCalls.Load(), string(rawMain(result.Input)))
	}
	t.Log("actual ordinary Agent: four physical model POSTs+child Read share cached parent Bearer; child completed-response header→parent mounted command JSON while next child POST held, subsequent headerless response clears process state")
}
