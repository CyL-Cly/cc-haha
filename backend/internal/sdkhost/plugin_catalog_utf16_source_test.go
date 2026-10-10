package sdkhost_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

func TestActualReachablePluginLoneUTF16FinalCatalogJSONMatchesSource(t *testing.T) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("actual pinned Source Bun required")
	}
	home := t.TempDir()
	put := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(home, ".claude", "settings.json"), []byte(`{}`))
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		manifest string
		files    map[string]string
	}{
		{`{"name":"lone-\ud800","commands":{"entry":{"source":"./entry.md","description":"ordinary description"}}}`, nil},
		{`{"name":"ordinary-high","commands":{"entry":{"source":"./entry.md","description":"description-\ud800"}}}`, nil},
		{`{"name":"ordinary-low","commands":{"entry":{"source":"./entry.md","description":"description-\udc00"}}}`, nil},
		{`{"name":"key-high","commands":{"named-\ud800":{"source":"./entry.md","description":"key description"}}}`, nil},
		{`{"name":"directory-\udc00","commands":"./commands"}`, map[string]string{"commands/namespace/entry.md": "Directory command body."}},
		{`{"name":"file-\ud800","commands":"./entry.md"}`, nil},
		{`{"name":"inline","commands":{"named-\udc00":{"content":"Inline command body.","description":"  trimmed-\ud800  "}}}`, nil},
		{`{"name":"skill-\ud800","skills":"./skills"}`, map[string]string{"skills/reader/SKILL.md": "---\nname: friendly\ndescription: Actual plugin skill description\nallowed-tools: Read\n---\nActual skill body; never execute."}},
		{`{"name":"fallback-cut","commands":"./entry.md"}`, map[string]string{"entry.md": strings.Repeat("a", 96) + "😀" + strings.Repeat("b", 6)}},
		{`{"name":"command-skill-\udc00","commands":"./commands"}`, map[string]string{"commands/reader/SKILL.md": "---\nname: friendly\ndescription: Actual command skill description\n---\nActual command skill body; never execute."}},
	}
	n, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: map[string]string{"HOME": home, "USERPROFILE": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"), "PATH": "/usr/bin:/bin"}, CWD: home, Platform: "darwin", ManagedRoot: filepath.Join(home, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, fmt.Errorf("owned Keychain unavailable")
	}
	settings, err := clibootstrap.NewNativeProcessSettings(n, "darwin", clibootstrap.DefaultState())
	if err != nil {
		t.Fatal(err)
	}
	defer settings.Close(context.Background())
	service, err := settings.SkillCommands(clibootstrap.NativeSkillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	creator, err := pluginregistry.NewCreator(nil)
	if err != nil {
		t.Fatal(err)
	}
	dirs := []string{}
	goJSON := []any{}
	manifests := []string{}
	goMetadata := []any{}
	for i, testCase := range cases {
		manifest := testCase.manifest
		manifests = append(manifests, manifest)
		dir := filepath.Join(home, fmt.Sprintf("plugin-%d", i))
		dirs = append(dirs, dir)
		put(filepath.Join(dir, ".claude-plugin", "plugin.json"), []byte(manifest))
		put(filepath.Join(dir, "entry.md"), []byte("Actual command body; never execute."))
		for path, content := range testCase.files {
			put(filepath.Join(dir, path), []byte(content))
		}
		p, errs, err := creator.Create(context.Background(), dir, "owned@inline", true, "fallback", true)
		if err != nil || len(errs) != 0 {
			t.Fatal("actual native manifest loader rejected", err, errs)
		}
		definitions := append(service.PluginCommands(context.Background(), []pluginregistry.Loaded{p}), service.PluginSkills(context.Background(), []pluginregistry.Loaded{p})...)
		if len(definitions) != 1 {
			t.Fatal("actual plugin command reader", len(definitions))
		}
		r := &sdkhost.PrintRegistry{}
		r.ReplaceNativePrompts(definitions, nil, false)
		metadata := []protocol.JSONObject{}
		for _, definition := range definitions {
			m := definition.Metadata()
			if string(m.Get("disableModelInvocation")) != "false" {
				t.Fatal("model invocation changed")
			}
			metadata = append(metadata, m)
		}
		listing, err := skillcommands.FormatWithinBudget(metadata, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		catalog := r.Catalog()
		goJSON = append(goJSON, map[string]any{
			"control":  string(auditRaw(r.ControlCommands()[:len(definitions)])),
			"system":   string(auditRaw(catalog.Commands[:len(definitions)])),
			"guide":    string(auditRaw(r.AgentGuideCommands()[:len(definitions)])),
			"metadata": string(auditRaw(metadata)), "skillListing": listing,
		})
		goMetadata = append(goMetadata, definitions[0].Metadata())
	}
	fixture, _ := filepath.Abs("testdata/plugin-catalog-utf16-source.fixture.ts")
	sandbox, _ := filepath.Abs("../../testdata/loopback-only.sb")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_LONE_CATALOG_PROBE=1", "ANTHROPIC_API_KEY=fake-lone-catalog", "ANTHROPIC_BASE_URL=http://127.0.0.1:9", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(auditRaw(map[string]any{"home": home, "directories": dirs, "goJSON": goJSON}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source physical loader/formatter", err, stderr.String())
	}
	var actual protocol.JSONObject
	if err = actual.UnmarshalJSON(data); err != nil {
		t.Fatal(err)
	}
	evidence := auditRaw(map[string]any{"actualSourcePhysicalLoaderAndJSONConsumer": actual, "actualGoNativeMetadata": goMetadata, "sourceManifestFiles": manifests})
	if path := os.Getenv("CC_HAHA_UTF16_EVIDENCE_PATH"); path != "" {
		stage, _ := filepath.Abs("../../..")
		if filepath.Dir(path) != stage {
			t.Fatal("evidence path escaped owned stage")
		}
		if err = os.WriteFile(path, append(evidence, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var rows []protocol.JSONObject
	if err = json.Unmarshal(actual.Get("rows"), &rows); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		var sourceUnits, goUnits any
		_ = json.Unmarshal(row.Get("sourceConsumerUnits"), &sourceUnits)
		_ = json.Unmarshal(row.Get("goConsumerUnits"), &goUnits)
		if !reflect.DeepEqual(sourceUnits, goUnits) {
			t.Errorf("reachable physical manifest case%d final control/system/guide/metadata/Skill JSON consumer differs\nSource%s\nGo%s", i, row.Get("sourceConsumerUnits"), row.Get("goConsumerUnits"))
		}
	}
	t.Logf("actual physical plugin loader cases=%d; Source final JSON charCodeAt, model-enabled raw metadata/Skill listing, system/control/guide catalog compared", len(rows))
}

func auditRaw(value any) []byte { data, _ := json.Marshal(value); return data }
