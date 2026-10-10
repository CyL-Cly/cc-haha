package cloudauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualGoogleAuthADCUserRefreshAndJWTHeadersTokenCachingAndUnchangedFiles(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	credential := map[string]any{"type": "authorized_user", "client_id": "CLIENT_PRIVATE", "client_secret": "SECRET_PRIVATE", "refresh_token": "REFRESH_PRIVATE", "quota_project_id": "PRIVATE_QUOTA", "unknown": map[string]any{"keep": true}}
	rows := []map[string]any{{"credentials": credential}}
	for _, missing := range []string{"client_id", "client_secret", "refresh_token"} {
		copy := map[string]any{}
		for key, value := range credential {
			if key != missing {
				copy[key] = value
			}
		}
		rows = append(rows, map[string]any{"credentials": copy})
	}
	rows = append(rows, map[string]any{"credentials": credential, "response": map[string]any{"expires_in": 1}}, map[string]any{"credentials": credential, "response": map[string]any{"token_type": "Custom"}}, map[string]any{"credentials": credential, "env": map[string]string{"GOOGLE_CLOUD_QUOTA_PROJECT": "ENV_QUOTA"}})
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	service := map[string]any{"type": "service_account", "client_email": "private@private-project.iam.gserviceaccount.com", "private_key": key, "private_key_id": "SOURCE_UNUSED_KEY_ID", "project_id": "KEY_PROJECT", "quota_project_id": "PRIVATE_QUOTA", "unknown": true}
	rows = append(rows, map[string]any{"credentials": service})
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	for _, row := range rows {
		row["now"] = now.UnixMilli()
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-gcp-adc.fixture.ts")
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
		path := filepath.Join(home, "credentials.json")
		original, _ := json.Marshal(row["credentials"])
		if err = os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{"HOME": home, "GOOGLE_CLOUD_PROJECT": "PRIVATE_PROJECT", "GOOGLE_APPLICATION_CREDENTIALS": path}
		if fields, ok := row["env"].(map[string]string); ok {
			for key, value := range fields {
				env[key] = value
			}
		}
		calls := []map[string]any{}
		auth, err := NewGoogleAuth(GoogleOptions{Environment: env, Home: home, CWD: home, Platform: "darwin", Now: func() time.Time { return now }, ReadFile: NativeAWSReadFile, Do: func(request *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(request.Body)
			calls = append(calls, map[string]any{"method": request.Method, "url": request.URL.String(), "data": string(body)})
			response := map[string]any{"access_token": "GOOGLE_ACCESS_PRIVATE", "token_type": "Bearer", "expires_in": 3600}
			if fields, ok := row["response"].(map[string]any); ok {
				for key, value := range fields {
					response[key] = value
				}
			}
			raw, _ := json.Marshal(response)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw))}, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		client, err := auth.GetClient(ctx)
		if err != nil {
			compareAWS(t, i, err.Error(), source.Rows[i]["error"])
			continue
		}
		headers := []map[string]string{}
		for n := 0; n < 2; n++ {
			header, e := client.GetRequestHeaders(ctx)
			if e != nil {
				t.Fatal(e)
			}
			values := map[string]string{}
			for key, value := range header {
				if len(value) > 0 {
					lower := "authorization"
					if key != "Authorization" {
						lower = "x-goog-user-project"
					}
					values[lower] = value[0]
				}
			}
			headers = append(headers, values)
		}
		project, err := auth.ProjectID(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var clientProject any
		if client.ProjectID != "" {
			clientProject = client.ProjectID
		}
		var observed map[string]json.RawMessage
		if json.Unmarshal(source.Rows[i]["value"], &observed) != nil {
			t.Fatal("Source value missing", string(source.Rows[i]["value"]))
		}
		compareAWS(t, i, client.Kind(), observed["client"])
		compareAWS(t, i, project, observed["projectId"])
		compareAWS(t, i, clientProject, observed["clientProjectId"])
		compareAWS(t, i, headers, observed["headers"])
		compareAWS(t, i, calls, observed["calls"])
		persisted, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(original, persisted) {
			t.Fatal("ADC files changed", err)
		}
	}
}
