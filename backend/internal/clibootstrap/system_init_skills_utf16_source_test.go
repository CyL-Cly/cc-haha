package clibootstrap

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualMainPrintSystemInitPluginSkillUTF16FinalJSONMatchesSource(t *testing.T) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("actual pinned Source Bun required")
	}
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
	f.write(t, filepath.Join(f.cwd, ".git", "HEAD"), "ref: refs/heads/private\n")
	dirs := []string{}
	for i, name := range []string{`"actual-high-\ud800"`, `"actual-low-\udc00"`} {
		dir := filepath.Join(f.home, fmt.Sprintf("inline-%d", i))
		dirs = append(dirs, dir)
		f.write(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":`+name+`}`)
		f.write(t, filepath.Join(dir, "skills", "reader", "SKILL.md"), "---\ndescription: Actual private Skill\nallowed-tools: Read\n---\nNever execute.")
	}
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "No model requests for local command", 500)
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	f.n.Environment["ANTHROPIC_API_KEY"] = "fake-skills-utf16"
	f.n.Environment["PATH"] = "/usr/bin:/bin"
	args, err := ParsePrintArguments(mainRecipeArgs(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	args.PluginDirectories = dirs
	var output, diagnostics bytes.Buffer
	input := strings.NewReader(`{"type":"user","message":{"role":"user","content":"/cost"}}` + "\n")
	m, err := NewMainPrint(MainPrintOptions{Arguments: args, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: input, Output: &output, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code, err := m.Run(ctx)
	if err != nil || code != 0 {
		t.Fatal(code, err, diagnostics.String())
	}
	if requests.Load() != 0 {
		t.Fatal("local command performed model work", requests.Load())
	}
	var init protocol.JSONObject
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	for {
		var row protocol.JSONObject
		err = decoder.Decode(&row)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if row.String("type") == "system" && row.String("subtype") == "init" {
			init = row
		}
	}
	if init.Get("skills") == nil {
		t.Fatal("actual SDK output missing system-init", output.String())
	}
	fixture, _ := filepath.Abs("testdata/system-init-skills-utf16-source.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/loopback-only.sb")
	child := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	child.Dir = f.home
	child.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_SKILLS_UTF16_FIXTURE=1", "ANTHROPIC_API_KEY=fake-skills-utf16", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_ENTRYPOINT=sdk-cli", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file"}
	child.Stdin = bytes.NewReader(rawMain(map[string]any{"home": f.home, "cwd": f.cwd, "directories": dirs, "goInitJSON": string(rawMain(init))}))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	data, err := child.Output()
	if err != nil {
		t.Fatal("actual Source system-init", err, stderr.String())
	}
	var actual protocol.JSONObject
	if err = actual.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	var source, goUnits any
	_ = json.Unmarshal(actual.Get("sourceSkillsUnits"), &source)
	_ = json.Unmarshal(actual.Get("goSkillsUnits"), &goUnits)
	if path := os.Getenv("CC_HAHA_UTF16_SKILLS_EVIDENCE_PATH"); path != "" {
		stage, _ := filepath.Abs("../../..")
		if filepath.Dir(path) != stage {
			t.Fatal("evidence escaped stage")
		}
		if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(source, goUnits) {
		t.Fatalf("actual system/init skills[] final JSON lost original units\nSource%s\nGo%s", actual.Get("sourceSkillsUnits"), actual.Get("goSkillsUnits"))
	}
	if requests.Load() != 0 {
		t.Fatal("Source or Go performed model requests", requests.Load())
	}
	t.Log("actual public CLI plugin loader/local /cost/Host init JSON and Source full catalog/system init match high+low; providerRequests=0")
}
