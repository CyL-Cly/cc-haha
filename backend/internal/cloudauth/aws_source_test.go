package cloudauth

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func awsSourceRows(t *testing.T, rows []map[string]any) []map[string]json.RawMessage {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-aws-primitives.fixture.ts")
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
	var source struct{ Rows []map[string]json.RawMessage }
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err)
	}
	return source.Rows
}

func compareAWS(t *testing.T, index int, got any, source json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err = json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(source, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("case%d Go%s Source%s", index, raw, source)
	}
}

func TestActualSmithySignatureV4CanonicalHeadersQueryPathsAndCredentials(t *testing.T) {
	token := "TOKEN_PRIVATE"
	credentials := AWSCredentials{AccessKeyID: "AKID_PRIVATE", SecretAccessKey: "SECRET_PRIVATE", SessionToken: &token}
	rows := []map[string]any{}
	paths := []string{"/", "/model/us.anthropic.claude%3Av1/invoke", "/a//b/../c/./", "/模型/with space/", "relative/../final/", ""}
	for i, path := range paths {
		rows = append(rows, map[string]any{"kind": "sign", "method": "POST", "path": path, "body": "{\"private\":true}", "headers": map[string]string{"Host": "bedrock-runtime.us-east-1.amazonaws.com", "X-Trim": " \t a\n b \u00a0", "User-Agent": "unowned user agent", "connection": "keep-alive", "Proxy-Authorization": "ignored", "sec-fetch-mode": "cors", "date": "old", "AUTHORIZATION": "old", "X-A": "first", "x-a": "later"}, "query": map[string][]string{"z": {"2", "1"}, "空 格": {"a+b", "!"}, "X-Amz-Signature": {"excluded"}, "empty": {}}, "credentials": credentials, "region": "us-east-1", "service": "bedrock", "now": "2026-10-08T01:02:03.000Z", "disableChecksum": i == 4, "disablePathEscape": i == 5})
	}
	rows = append(rows, map[string]any{"kind": "sign", "method": "GET", "path": "/", "body": "", "headers": map[string]string{"host": "sts.us-west-2.amazonaws.com", "X-Amz-Content-Sha256": "UNSIGNED-PAYLOAD"}, "credentials": AWSCredentials{AccessKeyID: "", SecretAccessKey: ""}, "region": "us-west-2", "service": "sts", "now": "2026-10-08T01:02:03.000Z"})
	source := awsSourceRows(t, rows)
	for i, row := range rows {
		now, _ := time.Parse(time.RFC3339, row["now"].(string))
		request := SigningRequest{Method: row["method"].(string), Path: row["path"].(string), Headers: row["headers"].(map[string]string), Body: []byte(row["body"].(string))}
		request.Query, _ = row["query"].(map[string][]string)
		options := SignOptions{Region: row["region"].(string), Service: row["service"].(string), Time: now}
		options.DisableChecksum, _ = row["disableChecksum"].(bool)
		options.DisablePathEscape, _ = row["disablePathEscape"].(bool)
		got := Sign(request, row["credentials"].(AWSCredentials), options)
		var original map[string]any
		_ = json.Unmarshal(source[i]["value"], &original)
		var expected map[string]string
		raw, _ := json.Marshal(original["headers"])
		_ = json.Unmarshal(raw, &expected)
		compareAWS(t, i, got.Headers, raw)
		if request.Headers["AUTHORIZATION"] == "old" && got.Headers["AUTHORIZATION"] != "" {
			t.Fatal("generated header retained")
		}
	}
}

func TestActualSmithyINIFilesAndAWSProfilePriority(t *testing.T) {
	iniRows := []map[string]any{}
	for _, data := range []string{"[default]\na=1\nb=one # comment\nc=semi;literal\n", "[profile 'quoted']\nx=y\n[sso-session foo]\na=b\n[other foo]\na=discard\n", "[a]\ns3 =\n  region = us-east-1\n  other=value\nplain=last\n", "[__proto__]\na=b\n", "[profile __proto__]\na=b\n", "[a]\nempty=\n\tchild=deep\nchild=flat\n"} {
		iniRows = append(iniRows, map[string]any{"kind": "ini", "data": data})
	}
	source := awsSourceRows(t, iniRows)
	for i, row := range iniRows {
		got, err := ParseINI(row["data"].(string))
		if err != nil {
			compareAWS(t, i, err.Error(), source[i]["error"])
		} else {
			compareAWS(t, i, got, source[i]["value"])
		}
	}
	rows := []map[string]any{
		{"kind": "default", "env": map[string]string{"AWS_ACCESS_KEY_ID": "ENV_PRIVATE", "AWS_SECRET_ACCESS_KEY": "ENV_SECRET"}},
		{"kind": "default", "env": map[string]string{"AWS_PROFILE": "named", "AWS_ACCESS_KEY_ID": "ENV_PRIVATE", "AWS_SECRET_ACCESS_KEY": "ENV_SECRET"}, "config": "[profile named]\naws_access_key_id=CONFIG_PRIVATE\naws_secret_access_key=CONFIG_SECRET\n", "credentialsFile": "[named]\naws_access_key_id=FILE_PRIVATE\naws_secret_access_key=FILE_SECRET\naws_session_token=FILE_TOKEN\n"},
		{"kind": "default", "env": map[string]string{}, "config": "[not-a-profile]\naws_access_key_id=DISCARD\naws_secret_access_key=DISCARD\n[default]\naws_access_key_id=DEFAULT_PRIVATE\naws_secret_access_key=DEFAULT_SECRET\naws_account_id=123456789012\n"},
		{"kind": "env", "env": map[string]string{"AWS_ACCESS_KEY_ID": "ENV_PRIVATE", "AWS_SECRET_ACCESS_KEY": "ENV_SECRET", "AWS_SESSION_TOKEN": "", "AWS_ACCOUNT_ID": "123", "AWS_CREDENTIAL_SCOPE": "scope", "AWS_CREDENTIAL_EXPIRATION": "2030-01-02T03:04:05.000Z"}},
		{"kind": "env", "env": map[string]string{"AWS_ACCESS_KEY_ID": "PARTIAL"}},
		{"kind": "default", "env": map[string]string{"AWS_EC2_METADATA_DISABLED": "true"}},
	}
	source = awsSourceRows(t, rows)
	for i, row := range rows {
		env := row["env"].(map[string]string)
		var got AWSCredentials
		var err error
		if row["kind"] == "env" {
			got, err = FromEnvironment(env)
		} else {
			home := t.TempDir()
			config := filepath.Join(home, "config")
			credentials := filepath.Join(home, "credentials")
			for key, path := range map[string]string{"config": config, "credentialsFile": credentials} {
				value, _ := row[key].(string)
				if err = os.WriteFile(path, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			frozen := map[string]string{"AWS_CONFIG_FILE": config, "AWS_SHARED_CREDENTIALS_FILE": credentials, "AWS_EC2_METADATA_DISABLED": "true"}
			for key, value := range env {
				frozen[key] = value
			}
			resolver, e := NewAWSResolver(AWSOptions{Environment: frozen, Home: home, ReadFile: NativeAWSReadFile})
			if e != nil {
				t.Fatal(e)
			}
			got, err = resolver.Resolve(context.Background())
		}
		if err != nil {
			compareAWS(t, i, err.Error(), source[i]["error"])
		} else {
			var projected map[string]any
			raw, _ := json.Marshal(got)
			_ = json.Unmarshal(raw, &projected)
			if got.Expiration != nil {
				projected["expiration"] = got.Expiration.UTC().Format("2006-01-02T15:04:05.000Z")
			}
			compareAWS(t, i, projected, source[i]["value"])
		}
	}
}
