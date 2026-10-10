package pluginschema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func manifests() []json.RawMessage {
	values := []string{
		`{"name":"sample","future":"strip"}`, `{"name":""}`, `{"name":"has spaces"}`, `{"name":false}`, `null`, `[]`, `{"version":"1"}`,
		`{"name":"sample","author":{"name":"owner","future":true}}`, `{"name":"sample","author":{"name":""}}`,
		`{"name":"sample","homepage":"https://example.invalid"}`, `{"name":"sample","homepage":"not-url"}`,
		`{"name":"sample","dependencies":["foo","bar@market@^1.0",{"name":"baz","marketplace":"market","version":"future"}]}`,
		`{"name":"sample","dependencies":["not valid"]}`,
		`{"name":"sample","commands":"./README.md","skills":["./skills/one","./skills/two"],"agents":"./a.md","workflows":"./workflows","outputStyles":"./style.txt"}`,
		`{"name":"sample","commands":"absolute"}`, `{"name":"sample","agents":"./folder"}`, `{"name":"sample","skills":[]}`,
		`{"name":"sample","commands":{"about":{"source":"./README.md","description":"about","unknown":true},"inline":{"content":"Instructions","allowedTools":["Read"]}}}`,
		`{"name":"sample","commands":{"invalid":{"source":"./x.md","content":"both"}}}`,
		`{"name":"sample","commands":{"invalid":{"content":""}}}`,
		`{"name":"sample","hooks":{"PreToolUse":[{"matcher":"Read","hooks":[{"type":"command","command":"echo offline","future":"strip"}]}],"FutureEvent":[]}}`,
		`{"name":"sample","hooks":"./hooks.json"}`, `{"name":"sample","hooks":["./hooks.json",{"Stop":[{"hooks":[{"type":"prompt","prompt":"Check"}]}]}]}`,
		`{"name":"sample","hooks":{"Stop":[{"hooks":[{"type":"unknown"}]}]}}`,
		`{"name":"sample","userConfig":{"apiKey":{"type":"string","title":"API key","sensitive":true,"default":""}}}`,
		`{"name":"sample","userConfig":{"1invalid":{"type":"string","title":"Key"}}}`,
		`{"name":"sample","userConfig":{"key":{"type":"string","title":"Key","unknown":true}}}`,
		`{"name":"sample","userConfig":{"key":{"type":"string","title":"Key","default":["one","two"],"required":true,"multiple":true}}}`,
		`{"name":"sample","channels":[{"server":"main","displayName":"Channel","userConfig":{"token":{"type":"string","title":"Token"}}}]}`,
		`{"name":"sample","channels":[{"server":"main","future":true}]}`,
		`{"name":"sample","mcpServers":{"main":{"command":"offline","future":true}}}`,
		`{"name":"sample","mcpServers":{"main":{"type":"http","url":"${user_config.url}","headers":{"Authorization":"fake"},"oauth":{"callbackPort":3000,"clientId":"id","authServerMetadataUrl":"https://example.invalid"}}}}`,
		`{"name":"sample","mcpServers":{"main":{"type":"http","url":"url","oauth":{"callbackPort":0}}}}`,
		`{"name":"sample","mcpServers":{"main":{"type":"http","url":"url","oauth":{"authServerMetadataUrl":"http://example.invalid"}}}}`,
		`{"name":"sample","mcpServers":["./config.json","./bundle.mcpb","https://example.invalid/bundle.dxt",{"main":{"type":"sdk","name":"offline"}}]}`,
		`{"name":"sample","mcpServers":"./wrong.txt"}`,
		`{"name":"sample","lspServers":{"main":{"command":"lsp","extensionToLanguage":{".go":"go"},"future":true}}}`,
		`{"name":"sample","lspServers":{"main":{"command":"lsp","extensionToLanguage":{".go":"go"}}}}`,
		`{"name":"sample","lspServers":{"main":{"command":"lsp --flag","extensionToLanguage":{"go":"go"}}}}`,
		`{"name":"sample","lspServers":{"main":{"command":"/a path/lsp","extensionToLanguage":{},"maxRestarts":-1,"startupTimeout":1.5}}}`,
		`{"name":"sample","settings":{"agent":"Plan","future":true}}`,
		`{"name":"sample","keywords":[1],"version":null,"description":false}`,
	}
	out := []json.RawMessage{}
	for _, value := range values {
		out = append(out, json.RawMessage(value))
	}
	return out
}
func TestManifestValidationAgainstActualProductionSchema(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun not configured")
	}
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "../../../scripts/quality-gate/go-parity/native-plugin-loader.fixture.ts")
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	input := manifests()
	body, _ := json.Marshal(map[string]any{"manifests": input})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(body)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_PLUGIN_LOADER_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("Source: %v %s", err, stderr.Bytes())
	}
	var expected struct{ Validated []json.RawMessage }
	if err = json.Unmarshal(stdout, &expected); err != nil {
		t.Fatalf("Source JSON: %v", err)
	}
	schemas, err := Declarations()
	if err != nil {
		t.Fatal(err)
	}
	for i, value := range input {
		t.Run(fmt.Sprintf("case_%02d", i), func(t *testing.T) {
			parsed, issues := Validate(schemas.Manifest, value)
			var actual any
			if len(issues) > 0 {
				actual = map[string]any{"issues": issues}
			} else {
				actual = map[string]any{"value": parsed}
			}
			raw, _ := json.Marshal(actual)
			var a, b any
			_ = json.Unmarshal(raw, &a)
			_ = json.Unmarshal(expected.Validated[i], &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("input=%s\nGo=%s\nSource=%s", value, raw, expected.Validated[i])
			}
		})
	}
}
