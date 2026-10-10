package clibootstrap

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeMDMSettingsSnapshotCacheRefreshDoesNotInstallUntilNotifier(t *testing.T) {
	var reads atomic.Int32
	var current *protocol.JSONObject
	n := &NativeBoundary{mdm: &nativeMDMCache{read: func(context.Context) (*protocol.JSONObject, *protocol.JSONObject, error) {
		reads.Add(1)
		return current, nil, nil
	}}}
	initial, err := n.settingsMDMSnapshot(context.Background(), false)
	if err != nil || string(initial) != `{"mdm":{},"hkcu":{}}` {
		t.Fatalf("Source EMPTY_RESULT.settings shape %s %v", initial, err)
	}
	if _, err = n.settingsMDMSnapshot(context.Background(), false); err != nil || reads.Load() != 1 {
		t.Fatal("cached MDM getter performed another physical read")
	}
	next := protocol.NewJSONObject()
	_ = next.UnmarshalJSON([]byte(`{"permissions":{"deny":["Write"]},"future":{"keep":true}}`))
	current = &next
	fresh, err := n.settingsMDMSnapshot(context.Background(), true)
	if err != nil || reads.Load() != 2 {
		t.Fatal("actual refresh did not read physical primitive")
	}
	still, err := n.settingsMDMSnapshot(context.Background(), false)
	if err != nil || string(still) != string(initial) {
		t.Fatal("refresh installed policy before change notifier")
	}
	if err = n.installSettingsMDMSnapshot(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	installed, err := n.settingsMDMSnapshot(context.Background(), false)
	if err != nil || string(installed) != string(fresh) || reads.Load() != 2 {
		t.Fatal("notifier did not install cached snapshot")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = n.installSettingsMDMSnapshot(ctx, json.RawMessage(`{"mdm":{},"hkcu":{}}`)); err == nil {
		t.Fatal("cancelled notifier replaced native cache")
	}
	after, _ := n.settingsMDMSnapshot(context.Background(), false)
	if string(after) != string(installed) {
		t.Fatal("cancelled update changed live cached policy")
	}
}
