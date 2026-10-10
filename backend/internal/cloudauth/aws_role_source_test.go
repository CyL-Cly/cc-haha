package cloudauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActualSourceAWSAssumeRoleProfilePriorityAndPhysicalSignedSTS(t *testing.T) {
	result := map[string]string{"accessKeyId": "ASSUMED_PRIVATE", "secretAccessKey": "ASSUMED_SECRET", "sessionToken": "ASSUMED_TOKEN"}
	rows := []map[string]any{}
	configs := []string{
		"[profile selected]\nrole_arn=arn:aws:iam::123456789012:role/private\nsource_profile=base\nrole_session_name=source-exact\nexternal_id=private external\nduration_seconds=900suffix\n[profile base]\naws_access_key_id=SOURCE_PRIVATE\naws_secret_access_key=SOURCE_SECRET\naws_session_token=SOURCE_TOKEN\n",
		"[profile selected]\nrole_arn=arn:aws:iam::123456789012:role/private\ncredential_source=Environment\nrole_session_name=source-exact\n",
		// In a nested source profile, static credentials win over role fields.
		"[profile selected]\nrole_arn=arn:aws:iam::123456789012:role/private\nsource_profile=base\nrole_session_name=source-exact\n[profile base]\nrole_arn=ignored\nsource_profile=missing\naws_access_key_id=SOURCE_PRIVATE\naws_secret_access_key=SOURCE_SECRET\n",
		"[profile selected]\nrole_arn=arn:aws:iam::123456789012:role/private\nsource_profile=base\nrole_session_name=source-exact\n[profile base]\ncredential_source=Environment\n",
	}
	for _, config := range configs {
		rows = append(rows, map[string]any{"kind": "default", "config": config, "env": map[string]string{"AWS_PROFILE": "selected", "AWS_ACCESS_KEY_ID": "ENV_PRIVATE", "AWS_SECRET_ACCESS_KEY": "ENV_SECRET"}, "roleResult": result})
	}
	source := awsSourceRows(t, rows)
	for index, row := range rows {
		home := t.TempDir()
		config := filepath.Join(home, "config")
		credentialsPath := filepath.Join(home, "credentials")
		if err := os.WriteFile(config, []byte(row["config"].(string)), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(credentialsPath, nil, 0600); err != nil {
			t.Fatal(err)
		}
		calls := []map[string]any{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			params, _ := url.ParseQuery(string(raw))
			captured := map[string]any{}
			for key, values := range params {
				if key == "Action" || key == "Version" {
					continue
				}
				if key == "DurationSeconds" {
					var number float64
					_ = json.Unmarshal([]byte(values[0]), &number)
					captured[key] = number
				} else {
					captured[key] = values[0]
				}
			}
			base := AWSCredentials{AccessKeyID: "SOURCE_PRIVATE", SecretAccessKey: "SOURCE_SECRET"}
			if index == 0 {
				token := "SOURCE_TOKEN"
				base.SessionToken = &token
			}
			if index == 1 || index == 3 {
				base = AWSCredentials{AccessKeyID: "ENV_PRIVATE", SecretAccessKey: "ENV_SECRET"}
			}
			stamp, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
			if err != nil {
				t.Error(err)
			}
			signed := Sign(SigningRequest{Method: r.Method, Path: "/", Headers: map[string]string{"host": r.Host, "content-type": r.Header.Get("Content-Type")}, Body: raw}, base, SignOptions{Region: "us-east-1", Service: "sts", Time: stamp})
			if signed.Headers["authorization"] != r.Header.Get("Authorization") {
				t.Error("wrong profile credential signed actual STS request")
			}
			calls = append(calls, map[string]any{"credentials": base, "params": captured})
			w.Header().Set("Content-Type", "text/xml")
			_, _ = io.WriteString(w, "<AssumeRoleResponse><AssumeRoleResult><Credentials><AccessKeyId>ASSUMED_PRIVATE</AccessKeyId><SecretAccessKey>ASSUMED_SECRET</SecretAccessKey><SessionToken>ASSUMED_TOKEN</SessionToken></Credentials></AssumeRoleResult></AssumeRoleResponse>")
		}))
		defer server.Close()
		env := map[string]string{"AWS_CONFIG_FILE": config, "AWS_SHARED_CREDENTIALS_FILE": credentialsPath, "AWS_PROFILE": "selected", "AWS_ACCESS_KEY_ID": "ENV_PRIVATE", "AWS_SECRET_ACCESS_KEY": "ENV_SECRET", "AWS_EC2_METADATA_DISABLED": "true"}
		sts := STS{Region: "us-east-1", Endpoint: server.URL, Do: server.Client().Do}
		resolver, err := NewAWSResolver(AWSOptions{Environment: env, Home: home, ReadFile: NativeAWSReadFile, STS: &sts})
		if err != nil {
			t.Fatal(err)
		}
		value, err := resolver.Resolve(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var expected map[string]json.RawMessage
		_ = json.Unmarshal(source[index]["value"], &expected)
		var combined map[string]any
		_ = json.Unmarshal(mustJSON(t, source[index]), &combined)
		compareAWS(t, index, map[string]any{"value": value, "calls": calls}, mustJSON(t, combined))
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
