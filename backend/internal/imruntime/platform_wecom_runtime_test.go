package imruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestActualWecomParentBareAckObjectUsesOriginalUserNoticeCoercion(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	frame := object(raw(map[string]any{"errcode": -1, "errmsg": "private failure", "headers": map[string]string{"req_id": "private"}}))
	want := runPlatformStageSource(t, "native-wecom-parent-error.fixture.ts", "CC_HAHA_WECOM_PARENT_ERROR_FIXTURE", map[string]any{"frame": frame}, home)
	if err := os.WriteFile(filepath.Join(configDir, "adapters.json"), []byte(`{"wecom":{"allowedUsers":["U1"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	first := true
	notices := []string{}
	runtime := NewChatRuntime(context.Background(), ChatRuntimeOptions{Config: Config{Home: home, ConfigDir: configDir}, Pairing: NewPairing(configDir, nil), Port: ChatPort{Platform: "wecom", LogPrefix: "[WeCom]", SendNotice: func(id, text string) error {
		if first {
			first = false
			return &WecomAckError{Frame: frame}
		}
		notices = append(notices, text)
		return nil
	}}})
	defer runtime.Close()
	if err := runtime.HandleInbound(InboundChatMessage{ChatID: "U1", UserID: raw("U1"), Text: "/help"}); err != nil {
		t.Fatal(err)
	}
	exactJSON(t, raw(notices), want, "actual original Wecom bare ACK parent notice")
}
