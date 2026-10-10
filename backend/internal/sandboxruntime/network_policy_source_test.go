package sandboxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestActualSourceSandboxNetworkCanonicalizationPolicyAndAsk(t *testing.T) {
	rows := []map[string]any{}
	for _, host := range []string{"allowed.test", "sub.allowed.test", "ALLOWED.TEST.", "denied.test", "127.1", "2130706433", "0x7f.0.0.1", "0:0:0:0:0:0:0:1", "[::1]", "::ffff:127.0.0.1", "::ffff:1.2.3.4%x.allowed.test", "evil\x00.allowed.test", "_record.test", "", "foo/bar", "foo:80", strings.Repeat("a", 256), "é.test"} {
		for _, ask := range []any{nil, false, true, "error"} {
			for _, config := range []any{nil, map[string]any{"allowedDomains": []string{"*.allowed.test", "allowed.test", "127.0.0.1", "::1"}, "deniedDomains": []string{"denied.test"}}, map[string]any{"allowedDomains": []string{"*.*", "*.test"}, "deniedDomains": []string{"*.allowed.test"}}} {
				r := map[string]any{"host": host, "port": 443, "config": config}
				if ask != nil {
					r["ask"] = ask
				}
				rows = append(rows, r)
			}
		}
	}
	fixture, _ := filepath.Abs("testdata/network-policy.fixture.ts")
	raw, _ := json.Marshal(map[string]any{"rows": rows})
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	type askCall struct {
		Host string
		Port int
	}
	var source []struct {
		Allowed, Valid bool
		Canonical      *string
		Calls          []askCall
	}
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		host := row["host"].(string)
		config := (*NetworkConfig)(nil)
		if row["config"] != nil {
			data, _ := json.Marshal(row["config"])
			config = &NetworkConfig{}
			json.Unmarshal(data, config)
		}
		calls := []askCall{}
		var ask func(context.Context, string, int) (bool, error)
		if value, ok := row["ask"]; ok {
			ask = func(_ context.Context, host string, port int) (bool, error) {
				calls = append(calls, askCall{host, port})
				if value == "error" {
					return false, errors.New("actual callback error")
				}
				allowed, _ := value.(bool)
				return allowed, nil
			}
		}
		allowed := FilterNetworkRequest(context.Background(), config, host, 443, ask)
		canonical, ok := CanonicalHost(host)
		var canonicalPointer *string
		if ok {
			canonicalPointer = &canonical
		}
		if allowed != source[i].Allowed || ValidHost(host) != source[i].Valid || !reflect.DeepEqual(canonicalPointer, source[i].Canonical) || !reflect.DeepEqual(calls, source[i].Calls) {
			t.Fatalf("Source%d host%q %+v;Go allowed%v valid%v canonical%v calls%v", i, host, source[i], allowed, ValidHost(host), canonicalPointer, calls)
		}
	}
}
