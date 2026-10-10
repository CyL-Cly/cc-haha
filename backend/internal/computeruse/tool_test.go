package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type nativeToolFixture struct {
	mu           sync.Mutex
	calls        []object
	proven       bool
	permitted    bool
	missingImage bool
	failCommand  string
	onCall       func(string)
	clock        time.Time
}

func (fixture *nativeToolFixture) invoke(ctx context.Context, command string, raw json.RawMessage) (json.RawMessage, error) {
	fixture.mu.Lock()
	fixture.calls = append(fixture.calls, object{"command": encode(command), "payload": append(json.RawMessage(nil), raw...)})
	callback := fixture.onCall
	fixture.mu.Unlock()
	if callback != nil {
		callback(command)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if fixture.failCommand == command {
		return nil, &NativeCommandError{"native stale snapshot", "stale_snapshot"}
	}
	switch command {
	case "check_permissions":
		return encode(map[string]any{"accessibility": fixture.permitted, "screenRecording": nil}), nil
	case "resolve_app_target":
		target := object{"pid": encode(42), "bundleId": encode("private.app"), "path": encode("/private/App.app"), "executablePath": encode("/private/App.app/Contents/MacOS/App")}
		if fixture.proven {
			target["launchTime"] = encode(17)
		}
		return encode(target), nil
	case "get_app_state":
		state := object{"axText": encode("App=private.app\n  g17:4 Button\n\n"), "appInstructions": encode("  Guide  \n"), "bundleId": encode("private.app")}
		if !fixture.missingImage {
			state["screenshot"] = encode(map[string]any{"base64": "fixture-image", "mimeType": "image/jpeg"})
		}
		return encode(state), nil
	case "list_apps":
		return json.RawMessage(`[{"id":"private.app","displayName":"Private App","isRunning":true,"future":17},{"bundleId":"old.app","displayName":"Older App"}]`), nil
	default:
		return json.RawMessage(`{}`), nil
	}
}
func nativeToolsFixture(t *testing.T, service *Service, id string) (*NativeTools, *nativeToolFixture) {
	t.Helper()
	if service == nil {
		service = fixtureService(t, fixtureOptions(t))
		if err := service.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true,"authorizedApps":[],"grantFlags":{"systemKeyCombos":false}}`)); err != nil {
			t.Fatal(err)
		}
	}
	fixture := &nativeToolFixture{proven: true, permitted: true, clock: time.Unix(17, 0)}
	tools, err := NewNativeTools(NativeToolsOptions{Service: service, SessionID: id, NativeCall: fixture.invoke, PermissionCard: func(context.Context) {}, Now: func() time.Time { return fixture.clock }, GrantFlags: func(context.Context) (json.RawMessage, error) {
		return encode(object{"systemKeyCombos": encode(false)}), nil
	}, FormatLockHeld: func(string) string {
		return "Another Claude session is currently using the computer. Wait for that session to finish, or find a non-computer-use approach."
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tools.Close(context.Background()) })
	return tools, fixture
}
func TestNativeToolsValidateBeforeIOAndNeverUseLegacyAppAuthorization(t *testing.T) {
	tools, fixture := nativeToolsFixture(t, nil, "session")
	for _, input := range []struct{ name, raw string }{{"click", `{"app":"Any","element_index":17}`}, {"click", `{"app":"Any","x":1}`}, {"get_app_state", `{"app":"Any","disableDiff":null}`}, {"press_key", `{"app":"Any","key":"shift+cmd+tab","systemKeyCombos":true}`}, {"sequence", `{"app":"Any","steps":[{"tool":"click","app":"override","x":1,"y":2}]}`}} {
		result, err := tools.Call(context.Background(), input.name, json.RawMessage(input.raw))
		if err != nil || !result.IsError || !result.NativeCallNotDispatched {
			t.Fatalf("validation %s=%+v err=%v", input.name, result, err)
		}
	}
	if len(fixture.calls) != 0 {
		t.Fatalf("invalid input used native IO: %s", encode(fixture.calls))
	}
	if _, err := os.Stat(tools.lockPath()); !os.IsNotExist(err) {
		t.Fatal("invalid request acquired native lease")
	}
	result, err := tools.Call(context.Background(), "click", json.RawMessage(`{"app":"Password Manager","element_index":"g17:4"}`))
	if err != nil || result.IsError {
		t.Fatalf("global consent gated application category: %+v %v", result, err)
	}
	if len(fixture.calls) != 3 {
		t.Fatalf("calls=%s", encode(fixture.calls))
	}
	payload, _ := decodeObject(fixture.calls[2]["payload"])
	if payload["app"] != nil || string(payload["pid"]) != "42" || string(payload["index"]) != `"g17:4"` {
		t.Fatalf("unresolved target dispatched: %s", encode(payload))
	}
	identity, _ := decodeObject(payload["expectedProcessIdentity"])
	if string(identity["pid"]) != "42" || string(identity["launchTime"]) != "17" {
		t.Fatalf("PID lifetime proof missing: %s", encode(payload))
	}
}
func TestNativeToolsStateFrameGuidanceOnceAndUnprovenMutationStops(t *testing.T) {
	tools, fixture := nativeToolsFixture(t, nil, "session")
	first, err := tools.Call(context.Background(), "get_app_state", json.RawMessage(`{"app":"Private App","disable_diff":true}`))
	if err != nil || first.IsError || len(first.Content) != 2 || first.App != "/private/App.app" {
		t.Fatalf("state=%+v err=%v", first, err)
	}
	if first.Content[1].MimeType != "image/jpeg" || !strings.Contains(first.Content[0].Text, "<app_specific_instructions>\nGuide\n") || strings.Contains(first.Content[0].Text, "\n\n</app_state>") {
		t.Fatalf("frame=%+v", first.Content)
	}
	second, err := tools.Call(context.Background(), "get_app_state", json.RawMessage(`{"app":"private.app"}`))
	if err != nil || strings.Contains(second.Content[0].Text, "app_specific_instructions") {
		t.Fatalf("repeated guidance=%+v err=%v", second, err)
	}
	fixture.proven = false
	before := len(fixture.calls)
	failed, err := tools.Call(context.Background(), "type_text", json.RawMessage(`{"app":"Private App","text":"typed"}`))
	if err != nil || !failed.IsError || !strings.Contains(failed.Content[0].Text, "did not prove") {
		t.Fatalf("unproven=%+v err=%v", failed, err)
	}
	if len(fixture.calls) != before+2 {
		t.Fatal("unproven mutation reached engine")
	}
	fixture.proven = true
	fixture.failCommand = "click"
	failed, err = tools.Call(context.Background(), "click", json.RawMessage(`{"app":"Private App","x":1,"y":2}`))
	if err != nil || failed.NativeError["nativeCode"] != "stale_snapshot" || failed.NativeError["name"] != "Error" {
		t.Fatalf("native failure=%+v err=%v", failed, err)
	}
}
func TestNativeToolsGlobalPhysicalLeaseBlocksOtherSessionAndCleansAtTurnEnd(t *testing.T) {
	first, _ := nativeToolsFixture(t, nil, "first")
	second, other := nativeToolsFixture(t, first.options.Service, "second")
	_, err := first.Call(context.Background(), "list_apps", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.lockPath()); !os.IsNotExist(err) {
		t.Fatal("list_apps acquired lease")
	}
	_, err = first.Call(context.Background(), "get_app_state", json.RawMessage(`{"app":"Private App"}`))
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := second.Call(context.Background(), "get_app_state", json.RawMessage(`{"app":"Private App"}`))
	if err != nil || !blocked.IsError || blocked.Telemetry["error_kind"] != "cu_lock_held" || len(other.calls) != 0 {
		t.Fatalf("second=%+v err=%v IO=%d", blocked, err, len(other.calls))
	}
	if err = first.EndTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(first.lockPath()); !os.IsNotExist(err) {
		t.Fatal("turn cleanup retained physical lease")
	}
	allowed, err := second.Call(context.Background(), "get_app_state", json.RawMessage(`{"app":"Private App"}`))
	if err != nil || allowed.IsError {
		t.Fatalf("released lease still blocked: %+v %v", allowed, err)
	}
	if err = first.EndTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if current := second.readLock(); current == nil || current.SessionID != "second" {
		t.Fatal("old owner released replacement session's lease")
	}
}
func TestNativeToolsMissingTCCProducesMappedErrorAndOneGuidedCard(t *testing.T) {
	tools, fixture := nativeToolsFixture(t, nil, "session")
	fixture.permitted = false
	var cards atomic.Int32
	tools.options.PermissionCard = func(context.Context) { cards.Add(1) }
	for range 2 {
		result, err := tools.Call(context.Background(), "get_app_state", json.RawMessage(`{"app":"Any App"}`))
		if err != nil || !result.IsError || result.NativeError["code"] != -10009 || !result.NativeCallNotDispatched {
			t.Fatalf("TCC=%+v err=%v", result, err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for cards.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if cards.Load() != 1 || len(fixture.calls) != 2 {
		t.Fatalf("cards=%d calls=%s", cards.Load(), encode(fixture.calls))
	}
}
func TestNativeToolsSequenceNeverRetriesAndReturnsFinalActualObservation(t *testing.T) {
	tools, fixture := nativeToolsFixture(t, nil, "session")
	input := json.RawMessage(`{"app":"Any App","steps":[{"tool":"click","x":1,"y":2},{"tool":"type_text","text":"one"}]}`)
	result, err := tools.Call(context.Background(), "sequence", input)
	if err != nil || result.IsError || result.StructuredContent["completedSteps"] != 2 || result.StructuredContent["status"] != "completed" || len(result.Content) != 3 {
		t.Fatalf("sequence=%+v err=%v", result, err)
	}
	commands := []string{}
	for _, call := range fixture.calls {
		command, _ := stringValue(call["command"])
		commands = append(commands, command)
	}
	if !reflect.DeepEqual(commands, []string{"check_permissions", "resolve_app_target", "click", "type_text", "get_app_state"}) {
		t.Fatalf("commands=%v", commands)
	}
	fixture.calls = nil
	fixture.failCommand = "type_text"
	result, err = tools.Call(context.Background(), "sequence", input)
	if err != nil || !result.IsError || result.StructuredContent["completedSteps"] != 1 || result.StructuredContent["failedStepIndex"] != 1 || result.StructuredContent["resultUnknown"] != true {
		t.Fatalf("failed sequence=%+v err=%v", result, err)
	}
	if len(fixture.calls) != 4 {
		t.Fatalf("sequence retried or observed after unknown mutation: %s", encode(fixture.calls))
	}
	fixture.failCommand = ""
	fixture.missingImage = true
	result, err = tools.Call(context.Background(), "sequence", input)
	if err != nil || !result.IsError || result.StructuredContent["status"] != "observation_failed" || result.StructuredContent["resultUnknown"] != false {
		t.Fatalf("missing screenshot=%+v %v", result, err)
	}
}
func TestNativeToolsCancelledQueueEntryCannotReleaseFollowingActionEarly(t *testing.T) {
	tools, fixture := nativeToolsFixture(t, nil, "session")
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var once sync.Once
	fixture.onCall = func(command string) {
		if command == "click" {
			once.Do(func() { close(entered); <-unblock })
		}
	}
	first := make(chan error, 1)
	go func() {
		_, err := tools.Call(context.Background(), "click", json.RawMessage(`{"app":"Any App","x":1,"y":2}`))
		first <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled, err := tools.Call(ctx, "click", json.RawMessage(`{"app":"Any App","x":3,"y":4}`))
	if err != nil || !cancelled.IsError {
		t.Fatalf("cancelled=%+v %v", cancelled, err)
	}
	last := make(chan error, 1)
	go func() {
		_, err := tools.Call(context.Background(), "click", json.RawMessage(`{"app":"Any App","x":5,"y":6}`))
		last <- err
	}()
	select {
	case <-last:
		t.Fatal("canceled waiter let a following native action bypass active call")
	case <-time.After(20 * time.Millisecond):
	}
	close(unblock)
	if err = <-first; err != nil {
		t.Fatal(err)
	}
	if err = <-last; err != nil {
		t.Fatal(err)
	}
}
func TestNativeLockOldMalformedAndStaleFixturesMatchRecoveryScope(t *testing.T) {
	tools, _ := nativeToolsFixture(t, nil, "session")
	for _, raw := range []string{`{broken`, `null`, `{"sessionId":"old","pid":2147483647,"future":17}`} {
		if err := os.WriteFile(tools.lockPath(), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		state, err := tools.acquireLock(context.Background())
		if err != nil || !state.Self {
			t.Fatalf("old lock=%s state=%+v err=%v", raw, state, err)
		}
		if err = tools.releaseLock(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Dir(tools.lockPath())
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 || entries[0].Name() != "cc-haha" {
		t.Fatalf("lock residue=%v err=%v", entries, err)
	}
	var missing *NativeCommandError
	if errors.As(errors.New("plain"), &missing) {
		t.Fatal("unexpected error mapping")
	}
}

func TestNativeCLIFeatureConsentIgnoresLegacyGrantFlagsAndNotifiesOnce(t *testing.T) {
	service := fixtureService(t, fixtureOptions(t))
	if err := service.UpdateConfig(context.Background(), json.RawMessage(`{"enabled":true,"authorizedApps":[],"grantFlags":{"systemKeyCombos":false,"clipboardRead":false,"clipboardWrite":false}}`)); err != nil {
		t.Fatal(err)
	}
	fixture := &nativeToolFixture{proven: true, permitted: true, clock: time.Unix(17, 0)}
	notifications := [][2]string{}
	tools, err := NewNativeTools(NativeToolsOptions{Service: service, SessionID: "cli-session", NativeCall: fixture.invoke, PermissionCard: func(context.Context) {}, OnNotification: func(_ context.Context, kind, message string) {
		notifications = append(notifications, [2]string{kind, message})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tools.Close(context.Background()) })
	for range 2 {
		result, err := tools.Call(context.Background(), "press_key", json.RawMessage(`{"app":"Password Manager","key":"cmd+q"}`))
		if err != nil || result.IsError {
			t.Fatalf("CLI changed single-consent grant policy: %+v %v", result, err)
		}
	}
	for _, row := range fixture.calls {
		name, _ := stringValue(row["command"])
		if name == "press_key" {
			payload, _ := decodeObject(row["payload"])
			if string(payload["systemKeyCombos"]) != "true" {
				t.Fatal("real CLI wrapper's granted system key flag was discarded")
			}
		}
	}
	if err = tools.EndTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = tools.EndTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"computer_use_enter", "Claude is using your computer · press Ctrl+C to stop"}, {"computer_use_exit", "Claude is done using your computer"}}
	if !reflect.DeepEqual(notifications, want) {
		t.Fatalf("notifications=%v", notifications)
	}
	if got := tools.options.FormatLockHeld("abcdefghijk"); got != "Computer use is in use by another Claude session (abcdefgh…). Wait for that session to finish or run /exit there." {
		t.Fatalf("Source CLI lease message=%s", got)
	}
}

func TestNativeToolsLeaseUsesActualResumedSessionIDAtCallTime(t *testing.T) {
	tools, fixture := nativeToolsFixture(t, nil, "startup-placeholder")
	var current atomic.Value
	current.Store("actual-resumed-session")
	tools.options.CurrentSessionID = func() string { return current.Load().(string) }
	result, e := tools.Call(context.Background(), "click", json.RawMessage(`{"app":"Any App","x":1,"y":2}`))
	if e != nil || result.IsError {
		t.Fatalf("call=%+v %v", result, e)
	}
	bytes, e := os.ReadFile(tools.lockPath())
	if e != nil {
		t.Fatal(e)
	}
	var lease struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(bytes, &lease)
	if lease.SessionID != "actual-resumed-session" {
		t.Fatalf("lease=%s", bytes)
	}
	if e := tools.EndTurn(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(tools.lockPath()); !os.IsNotExist(e) {
		t.Fatal("actual resumed lease not released")
	}
	current.Store("actual-second-session")
	result, e = tools.Call(context.Background(), "click", json.RawMessage(`{"app":"Any App","x":1,"y":2}`))
	if e != nil || result.IsError {
		t.Fatal(result, e)
	}
	bytes, e = os.ReadFile(tools.lockPath())
	if e != nil {
		t.Fatal(e)
	}
	_ = json.Unmarshal(bytes, &lease)
	if lease.SessionID != "actual-second-session" {
		t.Fatalf("second lease=%s", bytes)
	}
	if len(fixture.calls) != 6 {
		t.Fatalf("actual target proof/dispatch=%d", len(fixture.calls))
	}
}
