package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestTerminalTaskFooterHintActualPrivateGlobalSavePreservesUnknownAndExistingTruthyValue(t *testing.T) {
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, func(o *MainPrintOptions) { o.Interactive = true; o.Error = io.Discard })
	defer m.Close()
	for _, before := range []json.RawMessage{nil, []byte(`false`), []byte(`"already seen"`)} {
		if err := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			value := current.Clone()
			value.Set("unknownFooterFixture", json.RawMessage(`{"owned":true}`))
			if before == nil {
				value.Delete("hasSeenTasksHint")
			} else {
				value.Set("hasSeenTasksHint", before)
			}
			return &value, nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := m.MarkTerminalTasksHint(context.Background()); err != nil {
			t.Fatal(err)
		}
		value, err := m.Global.Get()
		if err != nil {
			t.Fatal(err)
		}
		unknown, exists := value.Object("unknownFooterFixture")
		if !exists || string(unknown.Get("owned")) != "true" {
			data, _ := value.MarshalJSON()
			t.Fatal(string(data), err)
		}
		want := `true`
		if nativeSettingsTruthy(before) {
			want = string(before)
		}
		if string(value.Get("hasSeenTasksHint")) != want {
			t.Fatal("Actual footer changed existing truthy value", value)
		}
		if err = m.MarkTerminalTasksHint(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
