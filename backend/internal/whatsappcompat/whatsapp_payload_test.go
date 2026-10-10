package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func TestActualOriginalRegistrationAndLoginPayloadVersionPropsAndLID(t *testing.T) {
	for _, paired := range []bool{false, true} {
		creds, e := CreateNodeCredentials()
		if e != nil {
			t.Fatal(e)
		}
		if paired {
			creds["me"] = nodeJSON(map[string]any{"id": "222:3@s.whatsapp.net"})
		}
		device, e := DeviceFromNodeCredentials(creds)
		if e != nil {
			t.Fatal(e)
		}
		version := [3]uint32{2, 3000, 777777}
		payload, e := proto.Marshal(NodeClientPayload(device, version))
		if e != nil {
			t.Fatal(e)
		}
		home := t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_payload.fixture.ts"))
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
		cmd.Stdin = bytes.NewReader(nodeJSON(map[string]any{"creds": creds, "version": version, "actual": base64.StdEncoding.EncodeToString(payload)}))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, e := cmd.Output()
		cancel()
		if e != nil {
			t.Fatal(e, stderr.String())
		}
		var result map[string]any
		if e = json.Unmarshal(out, &result); e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(result["expected"], result["actual"]) {
			t.Fatalf("actual original paired=%v payload fields differ", paired)
		}
	}
}

type versionRoute struct{ peer *httptest.Server }

func (r versionRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() != NodeWhatsAppVersionURL || req.Method != "GET" {
		return nil, fmt.Errorf("unowned version HTTP target")
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = "http"
	u.Host = r.peer.Listener.Addr().String()
	copy.URL = &u
	return r.peer.Client().Transport.RoundTrip(copy)
}
func TestActualVersionHTTPLineSevenAndFallback(t *testing.T) {
	for _, row := range []struct {
		status int
		body   string
		want   [3]uint32
	}{{200, "a\nb\nc\nd\ne\nf\nconst version = [2, 3000, 8899];\n", [3]uint32{2, 3000, 8899}}, {200, "const version = [2, 3000, 8899];\n", nodeWhatsAppFallbackVersion}, {500, "private failure", nodeWhatsAppFallbackVersion}} {
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(row.status)
			_, _ = w.Write([]byte(row.body))
		}))
		got := fetchNodeVersion(context.Background(), &http.Client{Transport: versionRoute{peer}})
		peer.Close()
		if got != row.want {
			t.Fatal("version HTTP/fallback", got, row.want)
		}
	}
}
