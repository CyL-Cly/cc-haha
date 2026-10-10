package sandboxruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func testObject(t *testing.T, raw string) protocol.JSONObject {
	t.Helper()
	var o protocol.JSONObject
	if e := o.UnmarshalJSON([]byte(raw)); e != nil {
		t.Fatal(e)
	}
	return o
}
func TestActualSourceSandboxConverterManagedPolicyRootsScrubAndUnknown(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	other := filepath.Join(home, "other")
	os.MkdirAll(cwd, 0700)
	os.MkdirAll(other, 0700)
	os.WriteFile(filepath.Join(cwd, "HEAD"), []byte("existing protected"), 0600)
	rows := []map[string]any{}
	inputs := []ConfigInput{}
	for _, raw := range []string{`{}`, `{"sandbox":{"network":{"allowedDomains":["direct.example","*.example"],"allowUnixSockets":[],"httpProxyPort":0,"socksProxyPort":1080},"enableWeakerNetworkIsolation":false,"ripgrep":{"command":"own-rg"}},"permissions":{"allow":["WebFetch(domain:allowed.example)","Bash(*)"],"deny":["WebFetch(domain:denied.example)","Read(/secret)"]}}`, `{"sandbox":{"ignoreViolations":{"*":[]},"network":{"allowAllUnixSockets":false,"allowLocalBinding":true}},"permissions":{"additionalDirectories":["one","one","two"]}}`, `{"sandbox":{"filesystem":{"allowWrite":["/literal","relative","//escape","~/home","é"],"denyRead":["//absolute"," /padded "],"allowRead":["a"],"denyWrite":["b"]}},"permissions":{"allow":["Edit(/settings-relative)","Edit(//absolute)","Edit(~/unexpanded)"],"deny":["Edit(./file)","Read(relative)"]}}`, `{"sandbox":{"filesystem":{"allowWrite":["null\u0000path"]}}}`} {
		for _, managed := range []bool{false, true} {
			for _, changed := range []bool{false, true} {
				settings := testObject(t, raw)
				policy := testObject(t, `{"sandbox":{"network":{"allowedDomains":["policy.example"]},"filesystem":{"allowRead":["policy-read"]}},"permissions":{"allow":["WebFetch(domain:policy-rule.example)"]}}`)
				if managed {
					sb := object(policy, "sandbox")
					net := object(sb, "network")
					net.Set("allowManagedDomainsOnly", []byte("true"))
					fs := object(sb, "filesystem")
					fs.Set("allowManagedReadPathsOnly", []byte("true"))
					n, _ := net.MarshalJSON()
					f, _ := fs.MarshalJSON()
					sb.Set("network", n)
					sb.Set("filesystem", f)
					b, _ := sb.MarshalJSON()
					policy.Set("sandbox", b)
				}
				sources := []SettingsSource{}
				sourceRows := []map[string]any{}
				for _, name := range []string{"userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"} {
					root := filepath.Join(home, name)
					os.MkdirAll(root, 0700)
					value := protocol.NewJSONObject()
					if name == "userSettings" {
						value = settings
					}
					if name == "policySettings" {
						value = policy
					}
					source := SettingsSource{Name: name, Root: root, File: filepath.Join(root, "settings.json"), Settings: value}
					sources = append(sources, source)
					sourceRows = append(sourceRows, map[string]any{"name": name, "root": root, "file": source.File, "settings": value})
				}
				current := cwd
				if changed {
					current = other
				}
				in := ConfigInput{Settings: settings, Sources: sources, Home: home, CWD: current, OriginalCWD: cwd, TemporaryDirectory: filepath.Join(home, "tmp") + string(filepath.Separator), ManagedDropInDirectory: filepath.Join(home, "managed.d"), AdditionalDirectories: []string{"two", "session-only"}, Ripgrep: testObject(t, `{"command":"rg","args":["--fixture"]}`), Stat: func(path string) error { _, e := os.Stat(path); return e }}
				if changed {
					in.MainWorktreeRepository = cwd
				}
				inputs = append(inputs, in)
				rows = append(rows, map[string]any{"settings": settings, "sources": sourceRows, "cwd": current, "originalCwd": cwd, "temporaryDirectory": in.TemporaryDirectory, "managedDropInDirectory": in.ManagedDropInDirectory, "additionalDirectories": in.AdditionalDirectories, "mainWorktreeRepository": in.MainWorktreeRepository})
			}
		}
	}
	fixture, _ := filepath.Abs("testdata/config.fixture.ts")
	input, _ := json.Marshal(map[string]any{"home": home, "rows": rows})
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []struct {
		Config     json.RawMessage
		ScrubPaths []string
		Error      *string
	}
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, in := range inputs {
		got, e := Convert(in)
		if source[i].Error != nil {
			if e == nil || e.Error() != *source[i].Error {
				t.Fatalf("Source error%d %v;Go %v", i, source[i].Error, e)
			}
			continue
		}
		if e != nil {
			t.Fatal(i, e)
		}
		encoded, _ := json.Marshal(got.Config)
		var a, b any
		json.Unmarshal(encoded, &a)
		json.Unmarshal(source[i].Config, &b)
		if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(got.ScrubPaths, source[i].ScrubPaths) {
			t.Fatalf("Source%d %s scrub%v;Go %s scrub%v", i, source[i].Config, source[i].ScrubPaths, encoded, got.ScrubPaths)
		}
	}
}
