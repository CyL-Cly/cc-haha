package imruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runDingTalkSource(t *testing.T, fixtureName string, input any) protocol.JSONObject {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("DingTalk Source requires explicitly pinned Bun")
	}
	fixture, e := filepath.Abs("../../testdata/" + fixtureName)
	if e != nil {
		t.Fatal(e)
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, "config"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_DINGTALK_PRIVATE_FIXTURE=1", "BUN_OPTIONS=--no-env-file"}
	if ca := object(raw(input)).String("ca"); ca != "" {
		path := filepath.Join(home, "ca.pem")
		if e = os.WriteFile(path, []byte(ca), 0600); e != nil {
			t.Fatal(e)
		}
		cmd.Env = append(cmd.Env, "NODE_EXTRA_CA_CERTS="+path)
	}
	cmd.Stdin = bytes.NewReader(raw(input))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatalf("DingTalk Source: %v %s", e, stderr.String())
	}
	if !json.Valid(data) {
		t.Fatalf("Source invalid JSON %q %s", data, stderr.String())
	}
	return object(data)
}
func TestDingTalkActualSourcePayloadPermissionCardsAndTableUTF16(t *testing.T) {
	payloads := []json.RawMessage{raw(nil), raw(false), raw(0), raw(1), raw("broken"), raw("null"), raw("[]"), raw("{}"), raw([]any{1, true}), json.RawMessage(`"{\"senderId\":\"\\ud800\",\"conversationType\":\"1\",\"text\":{\"content\":\"\\udfff\"}}"`)}
	for _, s := range []string{`{"conversationType":"1","senderStaffId":" U\ud800 ","senderId":"other","text":{"content":" \udfff 😀 "}}`, `{"conversationType":1,"senderStaffId":0,"senderId":true,"conversationId":1e400,"markdown":{"text":"md"}}`, `{"conversationType":"1","senderId":9007199254740993,"text":{"content":""},"markdown":{"text":"ignored"}}`, `{"msgtype":"picture","content":{"pictureUrl":" u\ud800 ","downloadCode":" code "}}`, `{"msgtype":"file","content":"{\"downloadCode\":\"code\",\"fileName\":\" x.pdf \"}"}`, `{"msgtype":"file","content":{"downloadCode":4,"fileName":"ignored"}}`, `{"msgtype":"richText","content":{"richText":[null,true,3,"abc",{"text":" a "},{"text":"😀\ud800","pictureUrl":"URL"},{"text":4,"downloadCode":"CODE"}]}}`, `{"content":{"text":4,"richText":[{"text":"a"},{"text":"b"}]}}`} {
		payloads = append(payloads, json.RawMessage(s))
	}
	actions := []json.RawMessage{raw(nil), raw("permit:id:yes"), json.RawMessage(`"permit:i\ud800:always"`), raw("permit:id:no"), raw("permit:id:bad"), raw(" permit:id:yes "), raw("{broken"), raw([]any{true, map[string]any{"request_id": " r ", "action": "allow-always"}})}
	for _, s := range []string{`{"requestId":" i\ud800 ","allowed":false,"rule":"always","outTrackId":" t ","conversationId":" c "}`, `{"requestId":9,"action":"yes"}`, `{"2":{"requestId":"two","action":"allow"},"1":{"requestId":"one","action":"deny"}}`, `{"requestId":"r","allowed":" yes ","action":"reject"}`, `{"requestId":"r","allow":"TrUe","action":"reject"}`, `{"requestId":"r","action":"deny","rule":"always"}`, `{"requestId":"outer","action":"allow","child":"permit:inner:no"}`, `"{\"requestId\":\"r\\ud800\",\"action\":\"permit\"}"`} {
		actions = append(actions, json.RawMessage(s))
	}
	params := []protocol.JSONObject{}
	for _, input := range []json.RawMessage{raw(nil), raw(false), raw("abc\n😀"), raw(map[string]any{"2": "x", "1": true, "body": strings.Repeat("中😀", 400)}), json.RawMessage(`{"input":"\ud800","number":1e400}`)} {
		params = append(params, protocol.NewJSONObject(dingTalkField("tool", Text{0xd800, 'W'}), protocol.JSONField{Name: "input", Value: input}, dingTalkField("request", Text{'r', 0xdfff})))
	}
	tables := []Text{}
	for _, s := range []string{"", "text\n|a|b|\n|---|:--:|\n|1|2|", "|x|\n|---|", "text\n\u00a0 |a|b| \u00a0\n\u00a0|---|---|\u00a0", "text\r\n|a|b|\r\n|---|---|\r", "text\nno table\n---", "text\n|a|\n|-|"} {
		tables = append(tables, NewText(s))
	}
	deliver := []protocol.JSONObject{}
	for _, target := range []string{`{"type":"user","userId":"u\ud800"}`, `{"type":"group","openConversationId":"g😀"}`} {
		deliver = append(deliver, protocol.NewJSONObject(dingTalkField("id", "fixed"), protocol.JSONField{Name: "target", Value: json.RawMessage(target)}, dingTalkField("robot", true)))
	}
	input := map[string]any{"payloads": payloads, "actions": actions, "params": params, "tables": tables, "deliver": deliver}
	source := runDingTalkSource(t, "dingtalk-leaves.fixture.ts", input)
	again := runDingTalkSource(t, "dingtalk-leaves.fixture.ts", input)
	exactJSON(t, raw(source), raw(again), "Source stable replay")
	gotPayload := []protocol.JSONObject{}
	for _, v := range payloads {
		p := ParseDingTalkPayload(v)
		out := protocol.NewJSONObject(dingTalkField("parsed", json.RawMessage("null")))
		if p != nil {
			out.Set("parsed", p)
			out.Set("direct", raw(IsDingTalkDirectMessage(p)))
			out.Set("sender", raw(DingTalkSenderID(p)))
			out.Set("chat", raw(DingTalkChatID(p)))
			out.Set("text", raw(ExtractDingTalkText(p)))
			out.Set("attachments", raw(ExtractDingTalkAttachments(p)))
		}
		gotPayload = append(gotPayload, out)
	}
	exactJSON(t, dingTalkWire(gotPayload), source.Get("payloads"), "payloads")
	gotActions := []*protocol.JSONObject{}
	for _, v := range actions {
		gotActions = append(gotActions, ParseDingTalkPermissionCardAction(v))
	}
	exactJSON(t, raw(gotActions), source.Get("actions"), "actions")
	gotParams := []protocol.JSONObject{}
	for _, v := range params {
		gotParams = append(gotParams, BuildDingTalkPermissionCardParams(TextJSON(v.Get("tool")), v.Get("input"), TextJSON(v.Get("request"))))
	}
	exactJSON(t, raw(gotParams), source.Get("params"), "params")
	gotTables := []Text{}
	for _, v := range tables {
		gotTables = append(gotTables, DingTalkTableBlankLines(v))
	}
	exactJSON(t, raw(gotTables), source.Get("tables"), "tables")
	gotDeliver := []protocol.JSONObject{}
	for _, v := range deliver {
		gotDeliver = append(gotDeliver, BuildDingTalkDeliverBody(TextJSON(v.Get("id")), object(v.Get("target")), v.Get("robot")))
	}
	exactJSON(t, raw(gotDeliver), source.Get("deliver"), "deliver")
}
