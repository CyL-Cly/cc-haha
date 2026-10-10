package computeruse

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestActualSourceComputerUseConfigCapabilityAndPermissionContracts(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source oracle requires pinned Bun 1.3.14")
	}
	options := fixtureOptions(t)
	repository, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	patches := []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{"enabled":true,"authorizedApps":[],"grantFlags":{"clipboardRead":false},"pythonPath":"\ufeff /python \u00a0"}`), json.RawMessage(`{"authorizedApps":[{"bundleId":" id ","displayName":" App ","authorizedAt":"","future":{"keep":17}}]}`), json.RawMessage(`{"pythonPath":null}`), json.RawMessage(`{"pythonPath":"\ufeff\u00a0 "}`), json.RawMessage(`{"enabled":1}`), json.RawMessage(`{"future":true}`), json.RawMessage(`{"grantFlags":{"future":true}}`), json.RawMessage(`{"authorizedApps":[{"bundleId":"x","displayName":"x","authorizedAt":17}]}`), json.RawMessage(`null`), json.RawMessage(`[]`)}
	stored := []json.RawMessage{json.RawMessage(`{"future":{"keep":17},"grantFlags":{"future":true,"clipboardRead":false},"authorizedApps":[{"bundleId":" id ","displayName":" App ","future":17}],"pythonPath":" /python "}`), json.RawMessage(`{}`), json.RawMessage(`{"pythonPath":null}`), json.RawMessage(`{"grantFlags":null}`), json.RawMessage(`{"enabled":"yes"}`)}
	versions := [][2]string{{"14.4", "14.4"}, {"14.3.9", "14.4"}, {"26.1", "14.4"}, {"14.4beta", "14.4"}, {"+14.4", "14.4"}, {"-14.4", "14.4"}, {"14", "14.4"}, {"foo.4", "14.4"}, {"14.4.0.1", "14.4"}, {"99999999999999999999999999999.0", "14.4"}}
	kernels := []string{"23.3.0", "23.4.0", "24.0.0", "24.x", "24", "+24.0", "26.5"}
	snapshots := []string{"", "noise\n{\"ok\":true,\"result\":{\"accessibility\":false,\"screenRecording\":true}}\nnoise", "{\"ok\":false,\"result\":{\"accessibility\":true}}", "{\"ok\":true,\"result\":{}}", "{\"ok\":true,\"result\":{\"accessibility\":true}}\n{\"ok\":true,\"result\":{\"accessibility\":false,\"screenRecording\":null}}"}
	sizes := []string{"", " ", "72", "15", "256.9", "0x20", "0b100000", "0o40", "2.5", "bad", "Infinity", "1_0", "0x1p2"}
	capabilities := []map[string]any{{"platform": "darwin", "version": "14.4", "helper": true, "kernel": true}, {"platform": "darwin", "version": "14.3", "helper": true, "kernel": true}, {"platform": "darwin", "version": nil, "helper": false, "kernel": true}, {"platform": "darwin", "version": nil, "helper": true, "kernel": false}, {"platform": "win32", "version": nil, "helper": false, "kernel": false}, {"platform": "linux", "version": nil, "helper": true, "kernel": true}}
	input := map[string]any{"patches": patches, "stored": stored, "versions": versions, "kernels": kernels, "snapshots": snapshots, "sizes": sizes, "capabilities": capabilities}
	script := `
const repository = Bun.argv[2]
const api = await import(repository + '/src/server/api/computer-use.ts')
const config = await import(repository + '/src/utils/computerUse/preauthorizedConfig.ts')
const helper = await import(repository + '/src/utils/computerUse/cuHelperBridge.ts')
const icons = await import(repository + '/src/server/services/macAppIcon.ts')
const input = await Bun.stdin.json()
const output = {
  patches: input.patches.map(api.parseComputerUseConfigPatch),
  stored: input.stored.map(config.parseStoredComputerUseConfig),
  versions: input.versions.map(([actual, floor]) => api.isVersionAtLeast(actual, floor)),
  kernels: input.kernels.map(release => helper.isMacosComputerUseRuntimeSupported('darwin', release)),
  snapshots: input.snapshots.map(api.parsePermissionSnapshot),
  sizes: input.sizes.map(icons.normalizeIconSize),
  capabilities: input.capabilities.map(c => api.resolveComputerUseCapability(c.platform, c.version, c.helper, c.kernel)),
}
console.log(JSON.stringify(output))
`
	file := filepath.Join(options.HomeDir, "source.ts")
	if err = os.WriteFile(file, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bun, file, repository)
	command.Dir = options.WorkDir
	for key, value := range options.Environment {
		command.Env = append(command.Env, key+"="+value)
	}
	command.Env = append(command.Env, "NODE_ENV=test")
	command.Stdin = strings.NewReader(string(encode(input)))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Source failed %v\n%s", err, output)
	}
	var source object
	if err = json.Unmarshal(output, &source); err != nil {
		t.Fatalf("Source=%s err=%v", output, err)
	}
	actual := object{}
	parsed := []any{}
	for _, raw := range patches {
		value, ok := parseConfig(raw, true)
		if !ok {
			parsed = append(parsed, nil)
		} else {
			parsed = append(parsed, value)
		}
	}
	actual["patches"] = encode(parsed)
	parsed = []any{}
	for _, raw := range stored {
		value, ok := parseConfig(raw, false)
		if !ok {
			parsed = append(parsed, nil)
		} else {
			parsed = append(parsed, value)
		}
	}
	actual["stored"] = encode(parsed)
	values := []bool{}
	for _, pair := range versions {
		values = append(values, IsVersionAtLeast(pair[0], pair[1]))
	}
	actual["versions"] = encode(values)
	values = []bool{}
	for _, release := range kernels {
		values = append(values, KernelEligible("darwin", release))
	}
	actual["kernels"] = encode(values)
	parsed = []any{}
	for _, text := range snapshots {
		result := ParsePermissionSnapshot(text)
		parsed = append(parsed, map[string]any{"accessibility": result.Accessibility, "screenRecording": result.ScreenRecording})
	}
	actual["snapshots"] = encode(parsed)
	integers := []int{}
	for _, size := range sizes {
		integers = append(integers, NormalizeIconSize(size))
	}
	actual["sizes"] = encode(integers)
	parsed = []any{}
	for _, capability := range capabilities {
		var version *string
		if text, ok := capability["version"].(string); ok {
			version = &text
		}
		parsed = append(parsed, ResolveCapability(capability["platform"].(string), version, capability["helper"].(bool), capability["kernel"].(bool)))
	}
	actual["capabilities"] = encode(parsed)
	for key, expected := range source {
		if !reflect.DeepEqual(jsonValue(t, actual[key]), jsonValue(t, expected)) {
			t.Errorf("Source %s=%s\nGo=%s", key, expected, actual[key])
		}
	}
}

func TestActualSourceOldConfigGETPUTAndMacStatus(t *testing.T) {
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source oracle requires pinned Bun 1.3.14")
	}
	options := fixtureOptions(t)
	repository, _ := filepath.Abs("../../..")
	service := fixtureService(t, options)
	old := `{"enabled":false,"authorizedApps":[{"bundleId":" private.app ","displayName":" Private App ","future":{"keep":17}}],"grantFlags":{"clipboardRead":false,"futureGrant":17},"pythonPath":" /custom/python ","future":{"keep":true}}`
	patch := `{"enabled":true,"authorizedApps":[],"grantFlags":{"systemKeyCombos":false},"pythonPath":null}`
	if err := os.MkdirAll(filepath.Dir(service.ConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(service.ConfigPath(), []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	script := `
const root = Bun.argv[2]
const api = await import(root + '/src/server/api/computer-use.ts')
const input = await Bun.stdin.json()
const url = new URL('http://localhost/api/computer-use/authorized-apps')
const before = await api.handleComputerUseApi(new Request(url), url, ['api', 'computer-use', 'authorized-apps'])
const response = await api.handleComputerUseApi(new Request(url,{method:'PUT',body:JSON.stringify(input.patch)}),url,['api','computer-use','authorized-apps'])
const after = await api.handleComputerUseApi(new Request(url),url,['api','computer-use','authorized-apps'])
const stored = JSON.parse(await Bun.file(input.configPath).text())
const states = []
for (const c of input.states) states.push(await api.checkStatus({platform:'darwin',arch:'arm64',detectMacosProductVersion:async()=>c.version,isMacosRuntimeSupported:()=>c.kernel,isCuHelperAvailable:()=>false}))
console.log(JSON.stringify({before:{status:before.status,body:await before.json()},put:{status:response.status,body:await response.json()},after:{status:after.status,body:await after.json()},stored,states}))
`
	states := []map[string]any{{"version": "14.3", "kernel": true}, {"version": "14.4", "kernel": true}, {"version": nil, "kernel": true}, {"version": nil, "kernel": false}}
	file := filepath.Join(options.HomeDir, "old-source.ts")
	_ = os.WriteFile(file, []byte(script), 0600)
	command := exec.Command(bun, file, repository)
	command.Dir = options.WorkDir
	for key, value := range options.Environment {
		command.Env = append(command.Env, key+"="+value)
	}
	command.Env = append(command.Env, "NODE_ENV=test")
	command.Stdin = strings.NewReader(string(encode(map[string]any{"patch": json.RawMessage(patch), "configPath": service.ConfigPath(), "states": states})))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Source=%s err=%v", output, err)
	}
	var expected object
	if json.Unmarshal(output, &expected) != nil {
		t.Fatalf("Source malformed=%s", output)
	}
	_ = os.WriteFile(service.ConfigPath(), []byte(old), 0600)
	actual := object{}
	status, raw := request(t, service, "GET", "authorized-apps", "")
	actual["before"] = encode(map[string]any{"status": status, "body": json.RawMessage(raw)})
	status, raw = request(t, service, "PUT", "authorized-apps", patch)
	actual["put"] = encode(map[string]any{"status": status, "body": json.RawMessage(raw)})
	status, raw = request(t, service, "GET", "authorized-apps", "")
	actual["after"] = encode(map[string]any{"status": status, "body": json.RawMessage(raw)})
	raw, _ = os.ReadFile(service.ConfigPath())
	actual["stored"] = raw
	resultStates := []object{}
	for _, state := range states {
		candidate := options
		candidate.Run = func(_ context.Context, file string, _ []string, _ []*os.File) (CommandResult, error) {
			if strings.HasSuffix(file, "sw_vers") {
				if version, ok := state["version"].(string); ok {
					return CommandResult{Stdout: version}, nil
				}
				return CommandResult{Code: 1}, nil
			}
			if state["kernel"].(bool) {
				return CommandResult{Stdout: "23.4.0"}, nil
			}
			return CommandResult{Stdout: "23.3.0"}, nil
		}
		controlled := fixtureService(t, candidate)
		result, err := controlled.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		resultStates = append(resultStates, result)
	}
	actual["states"] = encode(resultStates)
	for key, want := range expected {
		if !reflect.DeepEqual(jsonValue(t, actual[key]), jsonValue(t, want)) {
			t.Errorf("Source %s=%s\nGo=%s", key, want, actual[key])
		}
	}
}
