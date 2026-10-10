package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ioStep struct {
	Action   string              `json:"action"`
	Version  string              `json:"version,omitempty"`
	Options  ActionOptions       `json:"options"`
	Behavior protocol.JSONObject `json:"behavior"`
}
type scenario struct {
	Definition protocol.JSONObject `json:"definition"`
	Source     string              `json:"source,omitempty"`
	Actions    []ioStep            `json:"actions"`
}
type controlledIO struct {
	calls         [][]any
	behavior      protocol.JSONObject
	authenticated bool
	def           protocol.JSONObject
}

type controlledBridge struct{ *controlledIO }

func (c controlledBridge) Remove(context.Context, protocol.JSONObject) error {
	return c.step("remove-plugin")
}

func (c *controlledIO) step(name string, extra ...any) error {
	c.calls = append(c.calls, append([]any{name}, extra...))
	if c.behavior.String("fail") == name {
		return errors.New("offline-secret-must-never-be-exposed")
	}
	return nil
}
func (c *controlledIO) Configure(_ context.Context, config map[string]string) error {
	return c.step("configure", config)
}
func (c *controlledIO) Prepare(_ context.Context, p Progress) (Installation, error) {
	if err := c.step("prepare"); err != nil {
		return Installation{}, err
	}
	p("downloading", nil)
	return Installation{Directory: "/owned/connector", Command: "owned-cli", Args: []string{}, Environment: map[string]string{}}, nil
}
func (c *controlledIO) Authenticate(_ context.Context, _ Installation, p Progress) error {
	if err := c.step("authenticate"); err != nil {
		return err
	}
	url := "https://accounts.feishu.cn/login"
	p("awaiting-authorization", &url)
	c.authenticated = true
	return nil
}
func (c *controlledIO) Check(context.Context, Installation) (Check, error) {
	if err := c.step("check"); err != nil {
		return Check{}, err
	}
	verification := c.behavior.String("verification")
	if verification == "" {
		verification = "remote"
		if c.def.String("transport") == "skills" {
			verification = "local"
		}
	}
	check := Check{Authenticated: c.authenticated, Verification: verification}
	if c.authenticated {
		label := "Account"
		check.AccountLabel = &label
	}
	return check, nil
}
func (c *controlledIO) Deactivate(context.Context) error           { return c.step("deactivate") }
func (c *controlledIO) Remove(context.Context, Installation) error { return c.step("remove") }
func (c *controlledIO) Install(_ context.Context, d protocol.JSONObject, _ Installation) error {
	return c.step("install", d.String("version"))
}
func (c *controlledIO) SetEnabled(_ context.Context, _ protocol.JSONObject, v bool) error {
	return c.step("enabled", v)
}
func (c *controlledIO) Ready(context.Context, protocol.JSONObject) (bool, error) {
	return string(c.behavior.Get("ready")) != "false", c.step("ready")
}
func (c *controlledIO) Reload(_ context.Context, id string, required *protocol.JSONObject) error {
	var a, b any
	if id != "" {
		a = id
	}
	if required != nil {
		b = required.String("version")
	}
	return c.step("reload", a, b)
}

func connectorDef(transport, credentials string) protocol.JSONObject {
	return record(field("id", "sample"), field("pluginId", "office-sample@haha-connectors"), field("packageName", "offline"), field("version", "1.0.0"), field("homepage", "https://example.test"), field("transport", transport), field("credentialMode", credentials), field("platforms", []string{"darwin-arm64"}))
}

func sourceScenarios() []scenario {
	step := func(action string, b protocol.JSONObject) ioStep {
		return ioStep{Action: action, Behavior: b, Options: ActionOptions{AcknowledgeSharedCredentials: true, SessionID: "active"}}
	}
	flow := []ioStep{step("prepare", record()), step("authenticate", record()), step("check", record()), step("deactivate", record()), step("remove", record())}
	out := []scenario{{connectorDef("cli", "shared"), "", flow}, {connectorDef("mcp", "isolated"), "", flow}, {connectorDef("skills", "none"), "", flow}}
	for _, failure := range []string{"prepare", "install", "enabled", "reload", "configure", "authenticate", "check", "ready", "remove-plugin", "remove", "factory"} {
		action := "prepare"
		var actions []ioStep
		if failure == "authenticate" || failure == "check" || failure == "ready" {
			actions = append(actions, step("prepare", record()))
			action = "authenticate"
		}
		if failure == "remove-plugin" || failure == "remove" {
			actions = append(actions, step("prepare", record()), step("authenticate", record()))
			action = "remove"
		}
		last := step(action, record(field("fail", failure)))
		if failure == "configure" {
			last.Options.Configuration = map[string]string{"apiKey": "offline-key"}
		}
		actions = append(actions, last)
		out = append(out, scenario{connectorDef("mcp", "shared"), "", actions})
	}
	out = append(out,
		scenario{connectorDef("mcp", "shared"), "", []ioStep{step("prepare", record()), step("authenticate", record(field("authenticated", true))), step("check", record(field("authenticated", false)))}},
		scenario{connectorDef("mcp", "shared"), "", []ioStep{step("prepare", record()), step("authenticate", record(field("ready", false)))}},
		scenario{connectorDef("cli", "shared"), "", []ioStep{step("prepare", record()), step("authenticate", record(field("verification", "local")))}},
		scenario{connectorDef("skills", "none"), "", []ioStep{step("prepare", record(field("ready", false)))}},
	)
	upgrade := step("prepare", record(field("fail", "prepare")))
	upgrade.Version = "2.0.0"
	out = append(out, scenario{connectorDef("cli", "shared"), "", []ioStep{step("prepare", record()), step("authenticate", record()), upgrade}})
	return out
}

func TestLifecycleAndPersistenceMatchActualOriginalSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	clock, _ := time.Parse(time.RFC3339, "2026-10-06T01:02:03.000Z")
	cases := []struct {
		Source *string             `json:"source"`
		Patch  protocol.JSONObject `json:"patch"`
	}{}
	for _, text := range []string{`{`, `null`, `[]`, `{"schemaVersion":null,"connectors":{}}`, `{"schemaVersion":"1","connectors":{}}`, `{"connectors":[]}`, `{"connectors":{"sample":null}}`, `{"schemaVersion":1.0,"custom":{"n":9007199254740993,"units":"\ud800","html":"<keep>"},"connectors":{"sample":{"installed":0.0,"operation":0e0}}}`, `{"custom":"keep","connectors":{"sample":{"installed":true,"enabled":true,"operation":{"id":"old"},"verification":"remote","custom":"\udc00"}}}`} {
		v := text
		cases = append(cases, struct {
			Source *string             `json:"source"`
			Patch  protocol.JSONObject `json:"patch"`
		}{&v, record(field("status", "disabled"))})
	}
	scenarios := sourceScenarios()
	persistenceOutput := []any{}
	for i, item := range cases {
		root := filepath.Join(home, "go-persistence", string(rune('a'+i)))
		seedState(t, root, *item.Source)
		p, err := readPersistence(root, nil)
		if err != nil {
			persistenceOutput = append(persistenceOutput, map[string]any{"error": err.Error()})
			continue
		}
		before := p.get("sample")
		if err := p.patch("sample", item.Patch); err != nil {
			t.Fatal(err)
		}
		disk, err := os.ReadFile(p.path)
		if err != nil {
			t.Fatal(err)
		}
		persistenceOutput = append(persistenceOutput, map[string]any{"before": before, "after": p.get("sample"), "disk": string(disk)})
	}
	serviceOutput := []any{}
	for i, sc := range scenarios {
		root := filepath.Join(home, "go-services", string(rune('a'+i)))
		if sc.Source != "" {
			seedState(t, root, sc.Source)
		}
		defs := []protocol.JSONObject{sc.Definition.Clone()}
		io := &controlledIO{calls: [][]any{}, def: sc.Definition, authenticated: sc.Definition.String("transport") == "skills"}
		service, err := New(Options{Root: root, Platform: "darwin-arm64", Definitions: defs, Bridge: controlledBridge{io}, CreateAdapter: func(d protocol.JSONObject, _ string) (Adapter, error) {
			return io, io.step("factory", d.String("version"))
		}, Now: func() time.Time { return clock }, UUID: func() string { return "opaque" }})
		if err != nil {
			t.Fatal(err)
		}
		states := []any{}
		for _, a := range sc.Actions {
			io.behavior = a.Behavior
			if v := io.behavior.Get("authenticated"); v != nil {
				io.authenticated = string(v) == "true"
			}
			if a.Version != "" {
				defs[0].Set("version", quote(a.Version))
			}
			_, err := service.Action("sample", a.Action, a.Options)
			if err != nil {
				var status any
				var e *Error
				if errors.As(err, &e) {
					status = e.Status
				}
				states = append(states, map[string]any{"error": err.Error(), "status": status})
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = service.Wait(ctx, "sample")
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			state, err := service.Get("sample")
			if err != nil {
				t.Fatal(err)
			}
			states = append(states, map[string]any{"state": state})
		}
		disk, err := os.ReadFile(filepath.Join(root, "state.json"))
		if err != nil {
			t.Fatal(err)
		}
		serviceOutput = append(serviceOutput, map[string]any{"calls": io.calls, "states": states, "disk": string(disk)})
		if err := service.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	checks := []map[string]any{}
	for _, text := range []string{`{"identity":"user","verified":true}`, `{"identity":"user","verified":false}`, `{"identities":{"user":{"userName":"\ud800"}},"identity":"user","verified":true}`, `{"error":{"type":"config","subtype":"not_configured"}}`, `[]`, ``} {
		checks = append(checks, map[string]any{"id": "feishu", "result": map[string]any{"code": 0, "stdout": text, "stderr": ""}})
	}
	urls := []map[string]string{{"id": "feishu", "value": "https://ACCOUNTS.FEISHU.CN:443"}, {"id": "feishu", "value": "https://accounts.feishu.cn/a b"}, {"id": "wecom", "value": "https://work.weixin.qq.com/"}, {"id": "feishu", "value": "https://evil.test/"}}
	checkOutput := []any{}
	for _, c := range checks {
		r := c["result"].(map[string]any)
		check, err := ParseConnectorCheck("feishu", ProcessResult{Stdout: r["stdout"].(string)})
		if err != nil {
			checkOutput = append(checkOutput, map[string]any{"error": err.Error()})
		} else {
			checkOutput = append(checkOutput, map[string]any{"check": checkRecord(check)})
		}
	}
	urlOutput := []any{}
	for _, u := range urls {
		if v := TrustedAuthorizationURL(u["id"], u["value"]); v != "" {
			urlOutput = append(urlOutput, v)
		} else {
			urlOutput = append(urlOutput, nil)
		}
	}
	catalog, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	input := raw(map[string]any{"clock": clock.Format(time.RFC3339Nano), "persistence": cases, "services": scenarios, "checks": checks, "urls": urls})
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-connectors.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CONNECTOR_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	expected, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source child: %v; %s", err, stderr.String())
	}
	actual := raw(map[string]any{"persistence": persistenceOutput, "services": serviceOutput, "checks": checkOutput, "urls": urlOutput, "catalog": catalog})
	var a, b any
	if err = json.Unmarshal(actual, &a); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(expected, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		// Report each differing scenario, bounded to avoid dumping large pinned
		// catalogs or captured records into a command log.
		am, bm := a.(map[string]any), b.(map[string]any)
		for k, av := range am {
			bv := bm[k]
			if reflect.DeepEqual(av, bv) {
				continue
			}
			if al, ok := av.([]any); ok {
				bl, _ := bv.([]any)
				for i, item := range al {
					if i >= len(bl) || !reflect.DeepEqual(item, bl[i]) {
						got, want := string(raw(item)), "missing"
						if i < len(bl) {
							want = string(raw(bl[i]))
						}
						t.Errorf("%s[%d] Go=%s Source=%s", k, i, truncate(got, 2000), truncate(want, 2000))
					}
				}
			} else {
				t.Errorf("%s differs", k)
			}
		}
	}
	if strings.Contains(string(actual), "offline-secret-must-never-be-exposed") {
		t.Fatal("connector error exposed adapter output")
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
