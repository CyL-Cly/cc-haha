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
	"strings"
	"testing"
	"time"
)

func TestActualAWSIMDSTokenV1PolicyExpiryExtensionAndRetainedProviderState(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	credentials := map[string]any{"AccessKeyId": "IMDS_PRIVATE", "SecretAccessKey": "IMDS_SECRET", "Token": "IMDS_SESSION", "Expiration": "2030-01-02T03:04:05Z", "AccountId": "123"}
	row := func(responses ...map[string]any) map[string]any {
		return map[string]any{"now": now.UnixMilli(), "responses": responses}
	}
	body := func(value any) map[string]any { return map[string]any{"body": value} }
	failure := func(status int) map[string]any {
		return map[string]any{"error": "Error response received from instance metadata service", "status": status}
	}
	rows := []map[string]any{row(body("TOKEN_PRIVATE"), body(" role-private\n"), body(credentials)), row(failure(403), body("role-private"), body(credentials)), row(failure(403)), row(map[string]any{"error": "network-private"}, body("role-private"), body(credentials)), row(failure(400)), row(body("TOKEN_PRIVATE"), body("role-private"), body(map[string]any{"AccessKeyId": "bad"}))}
	rows[2]["env"] = map[string]string{"AWS_EC2_METADATA_V1_DISABLED": "true"}
	rows[3]["env"] = map[string]string{"AWS_EC2_METADATA_V1_DISABLED": "false"}
	expired := map[string]any{}
	for key, value := range credentials {
		expired[key] = value
	}
	expired["Expiration"] = "2020-01-02T03:04:05Z"
	rows = append(rows, row(body("TOKEN_PRIVATE"), body("role-private"), body(expired)))
	persist := row(failure(404), body("role-private"), body(credentials), body("role-private"), body(credentials))
	persist["count"] = 2
	rows = append(rows, persist)
	stable := row(body("TOKEN_PRIVATE"), body("role-private"), body(credentials), map[string]any{"error": "private unavailable"}, map[string]any{"error": "private unavailable"})
	stable["count"] = 2
	rows = append(rows, stable)
	configured := row(failure(405))
	configured["config"] = "[default]\nec2_metadata_v1_disabled=true\n"
	rows = append(rows, configured)
	overridden := row(failure(403), body("role-private"), body(credentials))
	overridden["config"] = configured["config"]
	overridden["env"] = map[string]string{"AWS_EC2_METADATA_V1_DISABLED": "false"}
	rows = append(rows, overridden)
	emptyToken := row(body(""), body("role-private"), body(credentials))
	emptyToken["env"] = map[string]string{"AWS_EC2_METADATA_V1_DISABLED": "true"}
	rows = append(rows, emptyToken)
	mode := row()
	mode["env"] = map[string]string{"AWS_EC2_METADATA_SERVICE_ENDPOINT": "", "AWS_EC2_METADATA_SERVICE_ENDPOINT_MODE": "invalid"}
	rows = append(rows, mode)
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-aws-imds.fixture.ts")
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
		home := t.TempDir()
		config := filepath.Join(home, "config")
		credentialsFile := filepath.Join(home, "credentials")
		text, _ := row["config"].(string)
		_ = os.WriteFile(config, []byte(text), 0600)
		_ = os.WriteFile(credentialsFile, nil, 0600)
		env := map[string]string{"AWS_CONFIG_FILE": config, "AWS_SHARED_CREDENTIALS_FILE": credentialsFile, "AWS_EC2_METADATA_SERVICE_ENDPOINT": "http://127.0.0.1:1"}
		if overlay, ok := row["env"].(map[string]string); ok {
			for key, value := range overlay {
				env[key] = value
			}
		}
		calls := []map[string]any{}
		cursor := 0
		responses := row["responses"].([]map[string]any)
		r, err := NewAWSResolver(AWSOptions{Home: home, Environment: env, ReadFile: NativeAWSReadFile, Now: func() time.Time { return now }, Metadata: &Metadata{Random: func() float64 { return .25 }, Do: func(request *http.Request) (*http.Response, error) {
			headers := map[string]string{}
			for key, values := range request.Header {
				headers[strings.ToLower(key)] = values[0]
			}
			calls = append(calls, map[string]any{"method": request.Method, "hostname": request.URL.Hostname(), "path": request.URL.Path, "headers": headers, "timeout": 1000})
			if cursor >= len(responses) {
				return nil, &MetadataError{Message: "unconfigured metadata primitive"}
			}
			step := responses[cursor]
			cursor++
			if message, present := step["error"]; present {
				status, _ := step["status"].(int)
				return nil, &MetadataError{Message: message.(string), Status: status}
			}
			raw, ok := step["body"].(string)
			if !ok {
				b, _ := json.Marshal(step["body"])
				raw = string(b)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(raw))}, nil
		}}})
		if err != nil {
			t.Fatal(err)
		}
		provider := NewInstanceProvider(r)
		count, _ := row["count"].(int)
		if count == 0 {
			count = 1
		}
		values := []map[string]any{}
		for n := 0; n < count; n++ {
			value, e := provider.Resolve(ctx)
			err = e
			if err != nil {
				break
			}
			projected := map[string]any{}
			b, _ := json.Marshal(value)
			_ = json.Unmarshal(b, &projected)
			if value.Expiration != nil {
				projected["expiration"] = value.Expiration.UTC().Format("2006-01-02T15:04:05.000Z")
			}
			if value.OriginalExpiration != nil {
				projected["originalExpiration"] = value.OriginalExpiration.UTC().Format("2006-01-02T15:04:05.000Z")
			}
			values = append(values, projected)
		}
		if err != nil {
			compareAWS(t, i, err.Error(), source.Rows[i]["error"])
		} else {
			compareAWS(t, i, values, source.Rows[i]["value"])
		}
		compareAWS(t, i, calls, source.Rows[i]["calls"])
	}
}
