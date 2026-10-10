package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualTerminalPermissionCycleChangesSameToolProducerAndKeepsOldConfig(t *testing.T) {
	n := nativeForTest(t)
	n.NativeProcess = true
	n.ConfigDirExplicit = true
	n.ManagedRoot = filepath.Join(n.Temp, "managed")
	_ = os.MkdirAll(n.ManagedRoot, 0700)
	n.Environment["PATH"] = "/usr/bin:/bin"
	n.Environment["USER_TYPE"] = "external"
	n.ResolvePolicy = func() (*protocol.JSONObject, error) { o := protocol.NewJSONObject(); return &o, nil }
	n.Now = func() time.Time { return time.UnixMilli(1744432123456) }
	global := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json")
	if e := os.MkdirAll(filepath.Dir(global), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(global, []byte(`{"futureGlobal":{"oldShape":[1,"keep"]},"lastPlanModeUse":1}`), 0600); e != nil {
		t.Fatal(e)
	}
	m := permissionRestartMain(t, n, true)
	if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		current, e := m.Global.Get()
		if e != nil {
			return e
		}
		if current.Get("futureGlobal") == nil || string(current.Get("lastPlanModeUse")) != "1" {
			return fmt.Errorf("old actual on-disk fixture was not loaded at %s", m.Global.Path())
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(n.CWD, "cycle-target.txt")
	_ = os.WriteFile(file, []byte("real existing"), 0600)
	permission := func() string {
		t.Helper()
		var behavior string
		if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			var e error
			behavior, e = m.nativeTools.Permission(scope, "Write", protocol.NewJSONObject(protocol.JSONField{Name: "file_path", Value: quoteMain(file)}))
			return e
		}); e != nil {
			t.Fatal(e)
		}
		return behavior
	}
	if behavior := permission(); behavior != "ask" {
		t.Fatal("actual initial Write producer not default", behavior)
	}
	before, _ := os.ReadFile(global)
	mode, e := m.CycleTerminalPermissionMode(context.Background())
	if e != nil || mode != "acceptEdits" || permission() != "allow" {
		t.Fatal("same actual tool producer did not change", mode, e)
	}
	after, _ := os.ReadFile(global)
	if !bytes.Equal(before, after) {
		t.Fatal("ordinary edit cycle wrote global")
	}
	mode, e = m.CycleTerminalPermissionMode(context.Background())
	if e != nil || mode != "plan" {
		t.Fatal(mode, e)
	}
	var current *protocol.JSONObject
	if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error { var e error; current, e = m.Global.Get(); return e }); e != nil {
		t.Fatal(e)
	}
	var compact bytes.Buffer
	if e = json.Compact(&compact, current.Get("futureGlobal")); e != nil {
		t.Fatal(e)
	}
	if string(current.Get("lastPlanModeUse")) != "1744432123456" || compact.String() != `{"oldShape":[1,"keep"]}` {
		t.Fatalf("original timestamp/old data not preserved timestamp=%s future=%s", current.Get("lastPlanModeUse"), current.Get("futureGlobal"))
	}
	physical, e := os.ReadFile(global)
	if e != nil {
		t.Fatal(e)
	}
	var persisted map[string]json.RawMessage
	if e = json.Unmarshal(physical, &persisted); e != nil {
		t.Fatal(e)
	}
	compact.Reset()
	if e = json.Compact(&compact, persisted["futureGlobal"]); e != nil {
		t.Fatal(e)
	}
	if string(persisted["lastPlanModeUse"]) != "1744432123456" || compact.String() != `{"oldShape":[1,"keep"]}` {
		t.Fatal("actual disk did not preserve stamp and future fields")
	}
	mode, e = m.CycleTerminalPermissionMode(context.Background())
	if e != nil || mode != "bypassPermissions" {
		t.Fatal("actual availability ignored", mode, e)
	}
	mode, e = m.CycleTerminalPermissionMode(context.Background())
	if e != nil || mode != "default" || permission() != "ask" {
		t.Fatal("same producer failed to restore default", mode, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = m.CycleTerminalPermissionMode(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled input accepted", e)
	}
	if mode, e = m.SessionPermissionMode(context.Background()); e != nil || mode != "default" {
		t.Fatal("cancel changed live permissions", mode, e)
	}
}
