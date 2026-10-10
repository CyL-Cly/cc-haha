package imruntime

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestQQOriginalC2CExtractionURLAndReconnectPolicies(t *testing.T) {
	pure := []map[string]any{}
	for _, kind := range []any{nil, "c2c", "group", "dm", "guild"} {
		for _, bot := range []bool{false, true} {
			pure = append(pure, map[string]any{"kind": "payload", "value": map[string]any{"kind": kind, "senderIsBot": bot, "senderId": " user ", "content": " text😀 ", "messageId": " id ", "timestamp": "stamp"}})
		}
	}
	for _, content := range []any{nil, "", " ", "😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀😀", " Chinese中文 ", json.RawMessage(`"\ud800 A"`)} {
		pure = append(pure, map[string]any{"kind": "payload", "value": map[string]any{"kind": "c2c", "senderId": " u ", "senderName": " name ", "content": content, "timestamp": "stamp", "attachments": []any{map[string]any{"url": "private.png", "filename": "image.png"}, map[string]any{"url": "voice", "asr_refer_text": " voice transcript "}, map[string]any{"url": "", "asr_refer_text": "ignored"}, map[string]any{"url": "voice2", "asr_refer_text": " "}}}})
	}
	for _, url := range []any{nil, "", "  ", "cdn.qq.test/private?q=x", "http://cdn.qq.test/a", "https://cdn.qq.test/a", "https://u:p@cdn.qq.test/a", "file:///tmp/a", "file:/tmp/a", "data:text/plain,x", "https:example", "http://cdn.qq.test:80/a", "//cdn.qq.test/a", "https://CDN.QQ.TEST/中文?x=中"} {
		pure = append(pure, map[string]any{"kind": "url", "value": url})
	}
	for _, code := range []int{1000, 1001, 1006, 4004, 4006, 4007, 4008, 4009, 4900, 4913, 4914, 4915, 4916} {
		for _, aborted := range []bool{false, true} {
			pure = append(pure, map[string]any{"kind": "reconnect", "actions": []map[string]any{{"kind": "connected", "now": 1000}, {"kind": "close", "code": code, "aborted": aborted, "now": 2000}, {"kind": "delay"}, {"kind": "delay"}, {"kind": "delay"}, {"kind": "delay"}, {"kind": "delay"}, {"kind": "delay"}, {"kind": "delay"}}})
		}
	}
	pure = append(pure, map[string]any{"kind": "reconnect", "actions": []map[string]any{{"kind": "connected", "now": 1000}, {"kind": "close", "code": 1006, "now": 1100}, {"kind": "connected", "now": 1200}, {"kind": "close", "code": 1006, "now": 1300}, {"kind": "connected", "now": 1400}, {"kind": "close", "code": 1006, "now": 1500}, {"kind": "delay"}, {"kind": "close", "code": 1006, "now": 10000}, {"kind": "delay", "delay": 12345}}})
	for _, data := range []map[string]any{{"author": map[string]any{"user_openid": "user"}, "content": "hello", "id": "id", "timestamp": "stamp"}, {"author": map[string]any{"user_openid": "user"}, "content": json.RawMessage(`"\ud800"`), "id": "id", "timestamp": "stamp", "message_scene": map[string]any{"source": "s", "ext": []any{"msg_idx=x", "ref_msg_idx=y", "msg_idx = second", "bad", 17, "ref_msg_idx="}}, "message_type": 103, "msg_elements": []any{map[string]any{"msg_idx": "quote"}, map[string]any{"msg_idx": "later"}}}} {
		pure = append(pure, map[string]any{"kind": "dispatch", "event": "C2C_MESSAGE_CREATE", "value": data})
	}
	result := object(qqOriginal(t, "native-qq-message-leaves.fixture.ts", map[string]any{"pure": pure}))
	want := collectJSONArray(result.Get("pure"))
	for i, p := range pure {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var value any
			switch p["kind"] {
			case "payload":
				v, e := ExtractQQPayload(object(raw(p["value"])))
				if e != nil {
					t.Fatal(e)
				}
				if v.IsObject() {
					value = v
				}
			case "url":
				if address, ok := NormalizeQQAttachmentURL(TextJSON(raw(p["value"]))); ok {
					value = address
				}
			case "dispatch":
				m, ok := QQDispatchC2C(p["event"].(string), object(raw(p["value"])))
				if !ok {
					t.Fatal("dispatch missing")
				}
				value = qqObject(qqField("action", "message"), qqField("msg", m))
			case "reconnect":
				state := QQReconnectState{}
				now := int64(100000000)
				values := []any{}
				for _, action := range p["actions"].([]map[string]any) {
					if n, ok := action["now"]; ok {
						now = int64(n.(int))
					}
					switch action["kind"] {
					case "connected":
						state.Connected(now)
						values = append(values, nil)
					case "delay":
						custom := 0
						if n, ok := action["delay"]; ok {
							custom = n.(int)
						}
						values = append(values, state.NextDelay(time.Duration(custom)*time.Millisecond).Milliseconds())
					default:
						aborted, _ := action["aborted"].(bool)
						a := state.HandleClose(action["code"].(int), aborted, now)
						v := qqObject(qqField("shouldReconnect", a.Reconnect), qqField("clearSession", a.ClearSession), qqField("refreshToken", a.RefreshToken), qqField("fatal", a.Fatal), qqField("reason", a.Reason))
						if a.Delay != 0 {
							v.Set("reconnectDelay", raw(a.Delay.Milliseconds()))
						}
						values = append(values, v)
					}
				}
				value = values
			}
			exactJSON(t, raw(qqObject(qqField("value", value))), raw(want[i]), fmt.Sprint(i))
		})
	}
	t.Logf("%d original adapter/vendor C2C/UTF16/URL/reconnect profiles", len(pure))
}
