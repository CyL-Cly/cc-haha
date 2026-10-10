package connectors

import (
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestAuthorizationURLUsesOriginalWHATWGSerialization(t *testing.T) {
	for _, tc := range []struct{ id, input, want string }{
		{"feishu", "https://ACCOUNTS.FEISHU.CN:443", "https://accounts.feishu.cn/"},
		{"feishu", "https://accounts.feishu.cn/a b", "https://accounts.feishu.cn/a%20b"},
		{"dingtalk", "https://mcp.dingtalk.com/login", "https://mcp.dingtalk.com/login"},
		{"wecom", "https://work.weixin.qq.com/", "https://work.weixin.qq.com/"},
		{"feishu", "https://accounts.feishu.cn.evil.test/", ""},
		{"feishu", "https://u:secret@accounts.feishu.cn/", ""},
		{"feishu", "https://accounts.feishu.cn:444/", ""},
		{"feishu", "http://accounts.feishu.cn/", ""},
	} {
		if got := TrustedAuthorizationURL(tc.id, tc.input); got != tc.want {
			t.Errorf("%s: %q; want %q", tc.input, got, tc.want)
		}
	}
}

func TestCheckRequiresActualVerificationAndSuccessFields(t *testing.T) {
	for _, tc := range []struct {
		id, stdout, stderr string
		code               int
		ok, auth           bool
		verification       string
	}{
		{"feishu", `{"identity":"user","verified":true}`, "", 0, true, true, "remote"},
		{"feishu", `{"identity":"app","verified":true}`, "", 0, true, false, "remote"},
		{"feishu", `{"identity":"user","verified":"true"}`, "", 0, true, false, "remote"},
		{"feishu", "", `{"error":{"type":"config","subtype":"not_configured"}}`, 1, true, false, "local"},
		{"feishu", "", `{"identity":"user","verified":true}`, 0, false, false, ""},
		{"dingtalk", `{"success":true,"authenticated":true}`, "", 0, true, true, "local"},
		{"dingtalk", `{"success":true,"authenticated":"true"}`, "", 0, false, false, ""},
		{"dingtalk", `{"success":false,"authenticated":true}`, "", 0, false, false, ""},
		{"wecom", "authorized\n", "", 0, true, true, "local"},
		{"wecom", "unauthorized\n", "", 0, true, false, "local"},
		{"wecom", `{"authenticated":true}`, "", 0, false, false, ""},
	} {
		got, err := ParseConnectorCheck(tc.id, ProcessResult{tc.code, tc.stdout, tc.stderr})
		if (err == nil) != tc.ok || err == nil && (got.Authenticated != tc.auth || got.Verification != tc.verification) {
			t.Errorf("%s stdout=%s check=%+v err=%v", tc.id, tc.stdout, got, err)
		}
	}
}

func TestAccountLabelKeepsOriginalTrimAndUTF16Slice(t *testing.T) {
	// JS removes control characters, trims ECMAScript whitespace and slices
	// code units. A pair cut at unit 160 remains a lone high surrogate in JSON.
	for _, name := range []string{`"\ufeff Alice\u0000\u007f \ufeff"`, `"` + strings.Repeat("a", 159) + `\ud83d\ude00"`, `"\ud800"`, `"\u0085Alice\u0085"`} {
		result, err := ParseConnectorCheck("feishu", ProcessResult{Stdout: `{"identity":"user","verified":true,"identities":{"user":{"userName":` + name + `}}}`})
		if err != nil {
			t.Fatal(err)
		}
		got := checkRecord(result).Get("accountLabel")
		want := name
		if strings.Contains(name, "Alice") && strings.Contains(name, "ufeff") {
			want = `"Alice"`
		} else if strings.Contains(name, "ud83d") {
			want = `"` + strings.Repeat("a", 159) + `\ud83d"`
		}
		got, _ = protocol.ParseAndStringifyJavaScriptJSON(got)
		wantJSON, _ := protocol.ParseAndStringifyJavaScriptJSON([]byte(want))
		if string(got) != string(wantJSON) {
			t.Errorf("label = %s; want %s", got, wantJSON)
		}
	}
}

func checkRecord(v Check) protocol.JSONObject {
	r := record(field("authenticated", v.Authenticated), field("verification", v.Verification))
	if v.AccountLabelJSON != nil {
		r.Set("accountLabel", v.AccountLabelJSON)
	} else if v.AccountLabel != nil {
		r.Set("accountLabel", quote(*v.AccountLabel))
	}
	return r
}
