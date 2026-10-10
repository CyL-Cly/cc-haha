package cloudauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualAWSSSONestedSDKRoleAndOIDCRequestSerialization(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	rows := []map[string]any{
		{"kind": "role", "region": "us-west-2", "input": map[string]string{"accountId": "123456789012", "roleName": "Private Role", "accessToken": "TOKEN_PRIVATE"}},
		{"kind": "role", "region": "cn-north-1", "input": map[string]string{"accountId": "123/+", "roleName": "角色@\" A", "accessToken": "TOKEN_第二"}},
		{"kind": "role", "region": "us-east-1", "input": map[string]string{"accountId": "123", "roleName": "", "accessToken": ""}},
		{"kind": "refresh", "region": "us-west-2", "input": map[string]string{"clientId": "C", "clientSecret": "S", "grantType": "refresh_token", "refreshToken": "R"}},
		{"kind": "refresh", "region": "cn-north-1", "input": map[string]string{"clientId": "C\n😀", "clientSecret": "S\"", "grantType": "refresh_token", "refreshToken": "R\\"}},
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-aws-sso-wire.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_AWS_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct{ Rows []map[string]json.RawMessage }
	if json.Unmarshal(out, &source) != nil {
		t.Fatal(string(out))
	}
	for i, row := range rows {
		var got map[string]any
		sso := SSO{RoleEndpoint: "http://127.0.0.1:1", OIDCEndpoint: "http://127.0.0.1:1", Do: func(r *http.Request) (*http.Response, error) {
			headers := map[string]string{}
			for _, key := range []string{"content-type", "x-amz-sso_bearer_token"} {
				if value := r.Header.Get(key); value != "" {
					headers[key] = value
				}
			}
			query := map[string]string{}
			for key, values := range r.URL.Query() {
				query[key] = values[0]
			}
			var body any
			if r.Body != nil {
				b, _ := io.ReadAll(r.Body)
				if len(b) > 0 {
					_ = json.Unmarshal(b, &body)
				}
			}
			got = map[string]any{"method": r.Method, "path": r.URL.Path, "query": query, "body": body, "headers": headers}
			result := `{"roleCredentials":{"accessKeyId":"A","secretAccessKey":"B","sessionToken":"C","expiration":1900000000000}}`
			if row["kind"] == "refresh" {
				result = `{"accessToken":"D","expiresIn":3600,"refreshToken":"E"}`
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(result))}, nil
		}}
		input := row["input"].(map[string]string)
		if row["kind"] == "role" {
			_, err = sso.role(ctx, row["region"].(string), input["accountId"], input["roleName"], input["accessToken"])
		} else {
			b, _ := json.Marshal(input)
			var token protocol.JSONObject
			_ = token.UnmarshalJSON(b)
			_, err = sso.refresh(ctx, row["region"].(string), token)
		}
		if err != nil {
			t.Fatal(err)
		}
		compareAWS(t, i, got, source.Rows[i]["value"])
	}
}
