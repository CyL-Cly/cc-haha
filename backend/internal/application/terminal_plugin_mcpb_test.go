package application

import (
	"archive/zip"
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

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func prepareTerminalMCPBFixture(t *testing.T, o Options) string {
	t.Helper()
	plugin := prepareTerminalPluginFixture(t, o)
	manifest := `{"name":"sample","description":"Actual bundled plugin","version":"1.0.0","mcpServers":["./ignored.json","./bundle.mcpb","./unused.dxt"]}`
	if err := os.WriteFile(filepath.Join(plugin, ".claude-plugin", "plugin.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(o.WorkDir, "actual-bundle-result.txt")
	bundleManifest := map[string]any{"manifest_version": "0.3", "name": "bundle-peer", "version": "1.0.0", "description": "actual private bundle", "author": map[string]any{"name": "Fixture"}, "server": map[string]any{"type": "python", "entry_point": "server.py", "mcp_config": map[string]any{"command": "/usr/bin/python3", "args": []string{"${__dirname}/server.py"}, "env": map[string]any{"WORD": "${user_config.word}", "TOKEN": "${user_config.token}", "OUTPUT": target, "PIDFILE": filepath.Join(o.WorkDir, "bundle-peer.pid")}}}, "user_config": map[string]any{"word": map[string]any{"type": "string", "title": "Word", "description": "actual result", "required": true}, "token": map[string]any{"type": "string", "title": "Token", "description": "private auth", "required": true, "sensitive": true}}}
	bundleManifest["user_config"] = obj([]byte(`{"word":{"type":"string","title":"Word","description":"actual result","required":true},"token":{"type":"string","title":"Token","description":"private auth","required":true,"sensitive":true}}`))
	python := `import json,os,sys
with open(os.environ['PIDFILE'],'w') as f:f.write(str(os.getpid()))
for line in sys.stdin:
 m=json.loads(line)
 if 'id' not in m:continue
 if m['method']=='initialize':r={'protocolVersion':'2025-11-25','capabilities':{'tools':{}},'serverInfo':{'name':'actual-bundle-peer','version':'1'}}
 elif m['method']=='tools/list':r={'tools':[{'name':'deliver','description':'Write actual configured bundled result','inputSchema':{'type':'object'}}]}
 elif m['method']=='tools/call':
  with open(os.environ['OUTPUT'],'w') as f:f.write(os.environ['WORD']+'|'+os.environ['TOKEN'])
  r={'content':[{'type':'text','text':'ACTUAL_BUNDLE_PHYSICAL_DELIVERY'}]}
 else:r={}
 print(json.dumps({'jsonrpc':'2.0','id':m['id'],'result':r}),flush=True)
`
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, data := range map[string][]byte{"manifest.json": raw(bundleManifest), "server.py": []byte(python)} {
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		header.SetMode(0644)
		w, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "bundle.mcpb"), buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return target
}

func TestTerminalPluginMCPBManageConfigurePhysicalDialogCacheStorageReloadToolAndNextHuman(t *testing.T) {
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := requests.Add(1)
		if n == 1 {
			tools := array(obj(body).Get("tools"))
			name := ""
			for _, tool := range tools {
				if strings.HasSuffix(tool.String("name"), "__deliver") {
					name = tool.String("name")
				}
			}
			if name == "" {
				t.Error("saved MCPB config absent from real next Harness catalog", string(body))
				response(w, "bundle-missing", "", nil, "MISSING")
				return
			}
			response(w, "bundle-tool", name, map[string]any{}, "")
		} else if n == 2 {
			if !strings.Contains(string(body), "ACTUAL_BUNDLE_PHYSICAL_DELIVERY") {
				t.Error("physical MCP tool result not consumed", string(body))
			}
			response(w, "bundle-complete", "", nil, "BUNDLE_COMPLETE")
		} else {
			response(w, "bundle-next-human", "", nil, "BUNDLE_NEXT_HUMAN")
		}
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Interactive = true
	o.Arguments.PermissionMode = "bypassPermissions"
	target := prepareTerminalMCPBFixture(t, o)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	var panel *tui.LocalPanel
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			copy := *event.LocalPanel
			panel = &copy
		}
		return nil
	}
	invoke := func(name, args string) {
		t.Helper()
		handled, err := s.InvokeTerminalCommand(ctx, name, args, emit)
		if !handled || err != nil {
			t.Fatal(name, handled, err)
		}
	}
	apply := func(action, row string, fields map[string]string) {
		t.Helper()
		if err := s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: action, Row: row, Fields: fields}, emit); err != nil || panel.Notice != "" && strings.Contains(panel.Notice, "Failed") {
			t.Fatal(action, err, panel)
		}
	}
	invoke("plugin", "install sample@mkt")
	apply("plugin-install-user", "install", nil)
	invoke("plugin", "manage")
	apply("plugin-open", panel.Rows[0].ID, nil)
	configured := false
	for _, row := range panel.Rows {
		if row.ID == "mcpb" && row.Label == "Configure" {
			configured = true
		}
		if row.ID == "options" {
			t.Fatal("top-level options row invented for MCPB-only manifest")
		}
	}
	if !configured {
		t.Fatal("real MCPB menu absent", panel)
	}
	apply("plugin-configure-mcpb", "mcpb", nil)
	if panel.Title != "Configure bundle-peer" || len(panel.Fields) != 2 || !panel.Fields[1].Sensitive {
		t.Fatal("forced real loader config missing", panel)
	}
	apply("save", "", map[string]string{"word": "FIRST_NATIVE_WORD", "token": "PRIVATE_BUNDLE_TOKEN"})
	if panel.Title != "sample@mkt" || len(panel.Fields) > 0 {
		t.Fatal("bundle save failed to return details", panel)
	}
	apply("plugin-configure-mcpb", "mcpb", nil)
	if panel.Fields[0].Value != "FIRST_NATIVE_WORD" || panel.Fields[1].Value != "" {
		t.Fatal("saved schema/presence not consumed by forced second mount", panel)
	}
	apply("plugin-back", "", nil)
	if panel.Title != "sample@mkt" {
		t.Fatal("cancel did not restore details", panel)
	}
	apply("plugin-configure-mcpb", "mcpb", nil)
	// Source dialog omits blank sensitive fields. The loader then returns
	// needs-config without saving; Manage still returns its save notice.
	apply("save", "", map[string]string{"word": "INVALID_BLANK_SECRET", "token": ""})
	settings, err := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json"))
	if err != nil || strings.Contains(string(settings), "INVALID_BLANK_SECRET") || strings.Contains(string(settings), "PRIVATE_BUNDLE_TOKEN") {
		t.Fatal("Source invalid provided config changed actual storage", string(settings), err)
	}
	apply("plugin-configure-mcpb", "mcpb", nil)
	apply("save", "", map[string]string{"word": "FINAL_NATIVE_WORD", "token": "PRIVATE_BUNDLE_TOKEN"})
	invoke("reload-plugins", "")
	if err = s.Submit(ctx, tui.Input{Text: "execute actual bundled tool", Mode: "prompt"}, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "FINAL_NATIVE_WORD|PRIVATE_BUNDLE_TOKEN" {
		t.Fatal("saved bundle failed actual tool consumer", string(data), err)
	}
	if err = s.Submit(ctx, tui.Input{Text: "next ordinary human", Mode: "prompt"}, nil); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 3 {
		t.Fatal("menu sent model or next Harness lifecycle changed", requests.Load())
	}
	credentials, err := os.ReadFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".credentials.json"))
	if err != nil || !strings.Contains(string(credentials), "sample@mkt/bundle-peer") || !strings.Contains(string(credentials), "PRIVATE_BUNDLE_TOKEN") {
		t.Fatal("bundle secret in wrong storage domain", err, string(credentials))
	}
}
