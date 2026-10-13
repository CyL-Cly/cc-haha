package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"testing"
)

func TestSyncFastACKAndRealMetadataWriterRetainExplicitOff(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	engine, e := NewSession(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer engine.Close(context.Background())
	var emitted json.RawMessage
	var saved json.RawMessage
	d := &DesktopSession{options: o, emit: func(data json.RawMessage) { emitted = append(json.RawMessage(nil), data...) }, updateLaunchMetadata: func(_ context.Context, data json.RawMessage) error {
		saved = append(json.RawMessage(nil), data...)
		return nil
	}}
	official := "openai-official"
	for _, enabled := range []bool{true, false} {
		next := providerconfig.RuntimeSelection{Selection: providerconfig.Selection{ProviderID: &official, ModelID: "gpt-6-sol", OpenAIFastMode: &enabled}}
		if e = d.persistRuntimeSelection(context.Background(), engine, next); e != nil {
			t.Fatal("real native Writer metadata", e)
		}
		d.broadcastAppliedRuntime(next)
		if string(obj(saved).Get("openAIFastMode")) != string(raw(enabled)) || string(obj(emitted).Get("openAIFastMode")) != string(raw(enabled)) {
			t.Fatal(string(saved), string(emitted))
		}
	}
	// A compatible provider cannot make the official tier effective or advertise
	// it in the acknowledgement, even if a stale caller carries that preference.
	compatible := "saved-deepseek"
	enabled := true
	next := providerconfig.RuntimeSelection{Selection: providerconfig.Selection{ProviderID: &compatible, ModelID: "deepseek-flash", OpenAIFastMode: &enabled}}
	if e = d.persistRuntimeSelection(context.Background(), engine, next); e != nil {
		t.Fatal(e)
	}
	d.broadcastAppliedRuntime(next)
	if string(obj(saved).Get("openAIFastMode")) != "false" || obj(emitted).Get("openAIFastMode") != nil {
		t.Fatal("official tier leaked", string(saved), string(emitted))
	}
}
