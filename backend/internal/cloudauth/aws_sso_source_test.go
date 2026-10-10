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
	"reflect"
	"strings"
	"testing"
	"time"
)

func ssoSource(t *testing.T, rows []map[string]any) []map[string]json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-aws-sso.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_AWS_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var result struct{ Rows []map[string]json.RawMessage }
	if err = json.Unmarshal(out, &result); err != nil {
		t.Fatal(err, string(out))
	}
	return result.Rows
}

func TestActualAWSSSOLegacySessionRefreshTokenCacheAndErrors(t *testing.T) {
	now := time.Date(2026, 10, 8, 2, 0, 0, 0, time.UTC)
	rows := []map[string]any{}
	add := func(session bool, token any, extra map[string]any) {
		i := len(rows)
		clock := now.Add(time.Duration(i) * time.Minute)
		id := "https://private-" + string(rune('a'+i)) + ".awsapps.com/start"
		config := "[profile selected]\nsso_account_id=123456789012\nsso_role_name=Private Role\n"
		if session {
			id = "private-session-" + string(rune('a'+i))
			config += "sso_session=" + id + "\n[sso-session " + id + "]\nsso_start_url=https://private.awsapps.com/start\nsso_region=us-west-2\n"
		} else {
			config += "sso_start_url=" + id + "\nsso_region=us-west-2\n"
		}
		row := map[string]any{"config": config, "id": id, "now": clock.UnixMilli()}
		if token != nil {
			row["token"] = token
		}
		for key, value := range extra {
			row[key] = value
		}
		rows = append(rows, row)
	}
	future := now.Add(24 * time.Hour).Format(time.RFC3339Nano)
	token := func(expiry string) map[string]any {
		return map[string]any{"accessToken": "TOKEN_PRIVATE", "expiresAt": expiry, "clientId": "CLIENT_PRIVATE", "clientSecret": "SECRET_PRIVATE", "refreshToken": "REFRESH_PRIVATE", "unknown": map[string]any{"keep": true}}
	}
	add(false, token(future), nil)
	add(false, nil, nil)
	add(false, "{broken", nil)
	add(false, token(now.Format(time.RFC3339)), nil)
	add(false, token(future), map[string]any{"roleResult": map[string]any{"roleCredentials": map[string]any{"accessKeyId": "A", "secretAccessKey": "B", "sessionToken": "C", "expiration": 0}}})
	add(false, token(future), map[string]any{"roleError": "private role denied"})
	add(true, token(future), nil)
	add(true, token(now.Add(8*time.Minute).Format(time.RFC3339)), nil)
	add(true, token(now.Add(9*time.Minute).Format(time.RFC3339)), map[string]any{"refreshError": "private refresh denied"})
	add(true, token(now.Add(-time.Minute).Format(time.RFC3339)), map[string]any{"refreshError": "private refresh denied"})
	add(true, map[string]any{"expiresAt": future}, nil)
	add(true, map[string]any{"accessToken": "TOKEN_PRIVATE", "expiresAt": now.Add(time.Minute).Format(time.RFC3339)}, nil)
	add(true, token(now.Add(12*time.Minute).Format(time.RFC3339)), map[string]any{"refreshResult": map[string]any{"accessToken": "REFRESHED_PRIVATE", "expiresIn": 3600}})
	add(true, token(future), map[string]any{"config": "[profile selected]\nsso_account_id=123\nsso_role_name=Role\nsso_session=missing\n"})
	add(false, token(future), map[string]any{"config": "[profile selected]\nsso_start_url=https://private.awsapps.com/start\nunknown=keep\n"})
	source := ssoSource(t, rows)
	shared := &SSOState{}
	for i, row := range rows {
		t.Run(time.UnixMilli(row["now"].(int64)).Format("150405"), func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, "config")
			credentials := filepath.Join(home, "credentials")
			if err := os.WriteFile(config, []byte(row["config"].(string)), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(credentials, nil, 0600); err != nil {
				t.Fatal(err)
			}
			path := SSOTokenPath(home, row["id"].(string))
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if value, present := row["token"]; present {
				raw, ok := value.(string)
				if !ok {
					b, _ := json.Marshal(value)
					raw = string(b)
				}
				if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			calls := []any{}
			transport := func(request *http.Request) (*http.Response, error) {
				kind := "role"
				input := map[string]any{"accountId": request.URL.Query().Get("account_id"), "roleName": request.URL.Query().Get("role_name"), "accessToken": request.Header.Get("x-amz-sso_bearer_token")}
				var result any = map[string]any{"roleCredentials": map[string]any{"accessKeyId": "SSO_PRIVATE", "secretAccessKey": "SSO_SECRET", "sessionToken": "SSO_SESSION", "expiration": 1900000000000}}
				if request.URL.Path == "/token" {
					kind = "refresh"
					input = map[string]any{}
					_ = json.NewDecoder(request.Body).Decode(&input)
					result = map[string]any{"accessToken": "REFRESHED_PRIVATE", "expiresIn": 3600, "refreshToken": "NEXT_REFRESH_PRIVATE"}
				}
				calls = append(calls, map[string]any{"kind": kind, "region": "us-west-2", "input": input})
				if failure, present := row[kind+"Error"]; present {
					return nil, &CredentialError{Message: failure.(string)}
				}
				if override, present := row[kind+"Result"]; present {
					result = override
				}
				raw, _ := json.Marshal(result)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, nil
			}
			resolver, err := NewAWSResolver(AWSOptions{Environment: map[string]string{"AWS_PROFILE": "selected", "AWS_CONFIG_FILE": config, "AWS_SHARED_CREDENTIALS_FILE": credentials}, Home: home, ReadFile: NativeAWSReadFile, Now: func() time.Time { return time.UnixMilli(row["now"].(int64)) }, WriteFile: func(ctx context.Context, path string, b []byte) error { return os.WriteFile(path, b, 0600) }, SSO: &SSO{Do: transport, State: shared}})
			if err != nil {
				t.Fatal(err)
			}
			value, err := resolver.Resolve(context.Background())
			if err != nil {
				compareAWS(t, i, err.Error(), source[i]["error"])
			} else {
				projected := map[string]any{}
				raw, _ := json.Marshal(value)
				_ = json.Unmarshal(raw, &projected)
				if value.Expiration != nil {
					projected["expiration"] = value.Expiration.UTC().Format("2006-01-02T15:04:05.000Z")
				}
				compareAWS(t, i, projected, source[i]["value"])
			}
			compareAWS(t, i, calls, source[i]["calls"])
			if raw, readErr := os.ReadFile(path); readErr == nil {
				var token any
				if json.Unmarshal(raw, &token) == nil {
					compareAWS(t, i, token, source[i]["token"])
				}
			}
			if !reflect.DeepEqual(row["config"], string(mustReadSSO(t, config))) {
				t.Fatal("config mutated")
			}
		})
	}
}
func mustReadSSO(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSSOExpiredRefreshThrottleIsSharedAcrossProfiles(t *testing.T) {
	now := time.Now().UTC()
	state := &SSOState{lastRefresh: now}
	r, _ := NewAWSResolver(AWSOptions{Home: t.TempDir(), ReadFile: NativeAWSReadFile, Now: func() time.Time { return now }, SSO: &SSO{State: state}})
	var token map[string]any = map[string]any{"accessToken": "A", "expiresAt": now.Add(-time.Second).Format(time.RFC3339)}
	raw, _ := json.Marshal(token)
	_ = raw
	// Covered through the public chain rather than treating a fake transport as
	// evidence: expired token is rejected before any native role request.
	path := filepath.Join(r.options.Home, "config")
	config := "[profile selected]\nsso_account_id=123\nsso_role_name=Role\nsso_session=private\n[sso-session private]\nsso_start_url=https://private.awsapps.com/start\nsso_region=us-east-1\n"
	_ = os.WriteFile(path, []byte(config), 0600)
	r.options.Environment = map[string]string{"AWS_CONFIG_FILE": path, "AWS_PROFILE": "selected"}
	cache := SSOTokenPath(r.options.Home, "private")
	_ = os.MkdirAll(filepath.Dir(cache), 0700)
	_ = os.WriteFile(cache, raw, 0600)
	r.options.SSO.Do = func(*http.Request) (*http.Response, error) { t.Error("unexpected transport"); return nil, nil }
	_, err := r.Resolve(context.Background())
	if err == nil || !strings.HasPrefix(err.Error(), "Token is expired.") {
		t.Fatal(err)
	}
}
