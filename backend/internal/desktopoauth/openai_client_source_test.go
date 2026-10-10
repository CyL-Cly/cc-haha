package desktopoauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestOpenAIClientMatchesActualBundledSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	clock := int64(1770000000000)
	jwt := func(claims string) string {
		return "fixture." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".signature"
	}
	rows := []map[string]any{}
	for _, input := range [][3]string{{"http://localhost:1455/auth/callback", "fixture-verifier", "fixture-state"}, {"https://example.invalid/a?b=界&c=~*", "verifier😃", "x+y/ z"}} {
		rows = append(rows, map[string]any{"kind": "authorize", "input": map[string]string{"redirectUri": input[0], "codeVerifier": input[1], "state": input[2]}})
	}
	tokens := []string{"", "broken", "x.%%%junk.y", jwt(`null`), jwt(`false`), jwt(`[]`), jwt(`"text"`), jwt(`{}`), jwt(`{"email":"fixture@invalid","chatgpt_account_id":"account-direct"}`), jwt(`{"chatgpt_account_id":"","https://api.openai.com/auth":{"chatgpt_account_id":"nested"},"organizations":[{"id":"org"}]}`), jwt(`{"organizations":[{"id":"org-first"},{"id":"org-second"}]}`), jwt(`{"email":"\ud800","chatgpt_account_id":false}`)}
	for _, token := range tokens {
		rows = append(rows, map[string]any{"kind": "claims", "token": token})
		for _, lifetime := range []any{nil, 0, 3600, "12", false} {
			response := map[string]any{"access_token": jwt(`{"email":"fallback@invalid","chatgpt_account_id":"fallback"}`), "refresh_token": "fixture-refresh", "id_token": token, "expires_in": lifetime}
			rows = append(rows, map[string]any{"kind": "normalize", "response": response})
			rows = append(rows, map[string]any{"kind": "refresh", "response": response, "existing": map[string]any{"refreshToken": "existing-refresh", "idToken": "existing-id", "accountId": "existing-account", "email": "existing@invalid", "clientId": "existing-client"}})
		}
	}
	for _, value := range []any{nil, "", false, 0, "fixture-refresh"} {
		rows = append(rows, map[string]any{"kind": "normalize", "response": map[string]any{"access_token": "fixture-access", "refresh_token": value}})
	}
	for _, expiry := range []any{clock + 300001, clock + 300000, clock + 299999, nil, "1770000300001", "bad", false} {
		rows = append(rows, map[string]any{"kind": "expired", "expiresAt": expiry})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-openai-client.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_OPENAI_CLIENT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"now": clock, "rows": rows})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %s", e, stderr.String())
	}
	var source struct{ Rows []json.RawMessage }
	if json.Unmarshal(output, &source) != nil || len(source.Rows) != len(rows) {
		t.Fatalf("Source rows %s", output)
	}
	for i, row := range rows {
		result := record()
		switch row["kind"] {
		case "authorize":
			input := row["input"].(map[string]string)
			result.Set("value", encode(BuildOpenAIAuthorizeURL(input["redirectUri"], input["codeVerifier"], input["state"])))
		case "claims":
			claims := ParseOpenAIJWTClaims(row["token"].(string))
			if claims != nil {
				result.Set("value", encode(claims))
			}
		case "normalize":
			value, err := NormalizeOpenAITokens(object(encode(row["response"])), clock)
			if err != nil {
				result.Set("error", encode(err.Error()))
			} else {
				result.Set("value", encode(value))
			}
		case "refresh":
			result.Set("value", encode(WithRefreshedOpenAIAccessToken(object(encode(row["existing"])), object(encode(row["response"])), clock)))
		case "expired":
			result.Set("value", encode(OpenAITokenExpired(encode(row["expiresAt"]), clock)))
		}
		var got, want any
		_ = json.Unmarshal(encode(result), &got)
		_ = json.Unmarshal(source.Rows[i], &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("row%d kind%v got%s want%s", i, row["kind"], encode(result), source.Rows[i])
		}
	}
}
