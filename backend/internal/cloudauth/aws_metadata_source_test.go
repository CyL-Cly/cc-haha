package cloudauth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualAWSHTTPMetadataURLGateAndCredentialValidationDates(t *testing.T) {
	rows := []map[string]any{}
	for _, value := range []string{"http://127.1/a?repeat=1&repeat=2", "http://localhost:123/a", "http://169.254.170.2/private", "http://169.254.170.23/a", "http://[fd00:ec2::23]/a", "http://[::1]/a", "http://127.255.255.255/a", "https://metadata.private.test/a", "http://unapproved.private.test/a", "http://169.254.169.254/a", "http://[fd00:ec2::254]/a", "https://user:ignored@metadata.private.test/private", "http://0x7f000001/a"} {
		rows = append(rows, map[string]any{"kind": "url", "url": value})
	}
	dates := []string{"2030-01-02T03:04:05.123456Z", "2030-01-02t03:04:05z", "2028-02-29T00:00:00Z", "2027-02-29T00:00:00Z", "2030-01-02T03:04:60Z", "2030-01-02T24:00:00Z", "2030-01-02T00:00:00+01:00", "2030-13-02T00:00:00Z", "2030-01-32T00:00:00Z", "2030-01-02T00:61:00Z", "0000-02-29T00:00:00Z", "0099-01-01T00:00:00Z", "0001-01-01T00:00:00Z"}
	for _, date := range dates {
		rows = append(rows, map[string]any{"body": map[string]any{"AccessKeyId": "A", "SecretAccessKey": "B", "Token": "C", "Expiration": date, "AccountId": "ignored modern"}})
	}
	rows = append(rows, map[string]any{"body": map[string]any{"AccessKeyId": "A"}}, map[string]any{"kind": "legacy", "body": map[string]any{"AccessKeyId": "A", "SecretAccessKey": "B", "Token": "C", "Expiration": "2030-01-02T03:04:05Z", "AccountId": "123"}}, map[string]any{"kind": "legacy", "body": map[string]any{"AccessKeyId": "", "SecretAccessKey": "", "Token": "", "Expiration": "2030-01-02T03:04:05Z"}})
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-aws-metadata.fixture.ts")
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
		if row["kind"] == "url" {
			target, err := CheckContainerURL(row["url"].(string))
			if err != nil {
				compareAWS(t, i, err.Error(), source.Rows[i]["error"])
				continue
			}
			parsed, _ := url.Parse(target)
			host := parsed.Hostname()
			if parsed.Host[0] == '[' {
				host = "[" + host + "]"
			}
			var port any
			if parsed.Port() != "" {
				var number json.Number = json.Number(parsed.Port())
				port = number
			}
			query := map[string]string{}
			for key, values := range parsed.Query() {
				query[key] = values[len(values)-1]
			}
			compareAWS(t, i, map[string]any{"method": "GET", "path": parsed.EscapedPath(), "query": query, "hostname": host, "protocol": parsed.Scheme + ":", "port": port}, source.Rows[i]["value"])
		} else {
			raw, _ := json.Marshal(row["body"])
			value, err := decodeMetadataCredentials(raw, row["kind"] != "legacy")
			if err != nil {
				compareAWS(t, i, err.Error(), source.Rows[i]["error"])
				continue
			}
			projected := map[string]any{}
			b, _ := json.Marshal(value)
			_ = json.Unmarshal(b, &projected)
			projected["expiration"] = value.Expiration.UTC().Format("2006-01-02T15:04:05.000Z")
			compareAWS(t, i, projected, source.Rows[i]["value"])
		}
	}
}
