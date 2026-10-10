package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func whatsAppOriginal(t *testing.T, fixture string, input any) json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("Actual Source Bun required")
	}
	home := t.TempDir()
	path, e := filepath.Abs("../../testdata/" + fixture)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(raw(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("actual Source %v\n%s", e, stderr.String())
	}
	return out
}
func TestWhatsAppActualSourceNormalizeTextFormatDisconnectUTF16(t *testing.T) {
	rows := []protocol.JSONObject{}
	add := func(op string, fields ...protocol.JSONField) {
		fields = append([]protocol.JSONField{{Name: "op", Value: raw(op)}}, fields...)
		rows = append(rows, protocol.NewJSONObject(fields...))
	}
	field := func(name string, v any) protocol.JSONField { return protocol.JSONField{Name: name, Value: raw(v)} }
	contents := []json.RawMessage{nil, raw(nil), raw(map[string]any{"conversation": "hello"}), raw(map[string]any{"conversation": "", "imageMessage": map[string]any{"caption": "image"}}), raw(map[string]any{"conversation": nil, "extendedTextMessage": map[string]any{"text": "extended"}}), raw(map[string]any{"imageMessage": map[string]any{"caption": "capt"}}), raw(map[string]any{"documentWithCaptionMessage": map[string]any{"message": map[string]any{"documentMessage": map[string]any{"caption": "doc"}}}})}
	for _, wrapper := range []string{"ephemeralMessage", "viewOnceMessage", "viewOnceMessageV2", "viewOnceMessageV2Extension", "editedMessage"} {
		contents = append(contents, raw(map[string]any{wrapper: map[string]any{"message": map[string]any{"conversation": "wrapped"}}}))
	}
	nested := raw(map[string]any{"conversation": "deep"})
	for i := 0; i < 7; i++ {
		nested = raw(map[string]any{"ephemeralMessage": map[string]any{"message": nested}})
		contents = append(contents, nested)
	}
	for _, v := range contents {
		add("normalize", protocol.JSONField{Name: "content", Value: v})
		add("text", protocol.JSONField{Name: "content", Value: v})
	}
	for _, jid := range []string{"1@s.whatsapp.net", "1@lid", "1@g.us", "status@broadcast", "a@broadcast", "@lid", "wrong"} {
		add("direct", field("jid", jid))
	}
	for _, text := range []Text{NewText("  hello  "), NewText("|A|B|\n|---|---|\n|one|two|"), NewText("```\n|A|B|\n|---|---|\n|one|two|\n```"), append(NewText("abc"), 0xd800, 0x20, 'x'), NewText("   ")} {
		add("format", field("text", text))
		for _, limit := range []int{1, 4, 4000} {
			add("split", field("text", text), field("limit", limit))
		}
	}
	for _, limit := range []int{-1, 0, 1, 4, 800} {
		add("preview", field("current", " \t"), field("delta", append(NewText("hello"), 0xd800)), field("limit", limit))
	}
	for _, e := range []any{nil, map[string]any{"statusCode": 401}, map[string]any{"output": map[string]any{"statusCode": 515}, "statusCode": 401}, map[string]any{"output": map[string]any{"statusCode": nil}, "statusCode": 408}, map[string]any{"statusCode": "401"}, map[string]any{"output": map[string]any{"statusCode": "401"}, "statusCode": 401}} {
		add("disconnect", field("error", e))
	}
	var wants []protocol.JSONObject
	if e := json.Unmarshal(whatsAppOriginal(t, "native-whatsapp-pure.fixture.ts", rows), &wants); e != nil {
		t.Fatal(e)
	}
	for i, row := range rows {
		var got any
		var value json.RawMessage
		switch row.String("op") {
		case "normalize":
			value = NormalizeWhatsAppContent(row.Get("content"))
		case "text":
			value = WhatsAppText(row.Get("content"))
		case "direct":
			got = WhatsAppDirectChat(TextJSON(row.Get("jid")))
		case "format":
			got = FormatWhatsAppOutbound(TextJSON(row.Get("text")))
		case "split":
			got = SplitWhatsAppText(TextJSON(row.Get("text")), qqInt(row.Get("limit"), 0))
		case "preview":
			full, msg := WhatsAppThinkingPreview(TextJSON(row.Get("current")), TextJSON(row.Get("delta")), qqInt(row.Get("limit"), 800))
			got = protocol.NewJSONObject(field("fullText", full), field("messageText", msg))
		case "disconnect":
			value = WhatsAppDisconnectStatus(row.Get("error"))
		}
		if got != nil {
			value = raw(got)
		}
		out := protocol.NewJSONObject(protocol.JSONField{Name: "value", Value: value})
		if !bytes.Equal(raw(out), raw(wants[i])) {
			t.Fatalf("case%d %s\nGo %s\nSource %s", i, raw(row), raw(out), raw(wants[i]))
		}
	}
	t.Logf("%d actual Source profiles", len(rows))
}

func TestWhatsAppActualSourceInboundDirectFilterDedupIdentityAndUTF16(t *testing.T) {
	rows := []protocol.JSONObject{}
	for _, jid := range []string{"1@s.whatsapp.net", "1@lid", "1@g.us", "status@broadcast", "1@broadcast", ""} {
		for _, from := range []bool{false, true} {
			rows = append(rows, object(raw(map[string]any{"op": "inbound", "message": map[string]any{"key": map[string]any{"remoteJid": jid, "id": "private-message", "fromMe": from}, "pushName": "private", "message": map[string]any{"conversation": "fixture text"}}})))
		}
	}
	for _, v := range []json.RawMessage{raw(nil), raw(map[string]any{}), raw(map[string]any{"key": map[string]any{"remoteJid": "1@lid", "id": 9}, "message": map[string]any{"conversation": ""}}), raw(map[string]any{"key": map[string]any{"remoteJid": 42, "id": "id"}, "message": map[string]any{"conversation": "abc"}}), raw(map[string]any{"key": map[string]any{"remoteJid": "1@lid", "id": "id"}, "message": map[string]any{"conversation": 9}}), []byte(`{"key":{"remoteJid":"a\ud800@lid","id":"b\udfff"},"message":{"ephemeralMessage":{"message":{"conversation":"x\ud800"}}}}`)} {
		rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "op", Value: raw("inbound")}, protocol.JSONField{Name: "message", Value: v}))
	}
	var wants []protocol.JSONObject
	if e := json.Unmarshal(whatsAppOriginal(t, "native-whatsapp-pure.fixture.ts", rows), &wants); e != nil {
		t.Fatal(e)
	}
	for i, row := range rows {
		v, e := WhatsAppInbound(object(row.Get("message")))
		value := raw(v)
		if !v.IsObject() {
			value = raw(nil)
		}
		out := protocol.NewJSONObject(protocol.JSONField{Name: "value", Value: value})
		if e != nil {
			out = protocol.NewJSONObject(protocol.JSONField{Name: "error", Value: raw(e.Error())})
		}
		if !bytes.Equal(raw(out), raw(wants[i])) {
			t.Fatalf("case%d Go%s Source%s", i, raw(out), raw(wants[i]))
		}
	}
	t.Logf("%d actual original handler metadata profiles", len(rows))
}
