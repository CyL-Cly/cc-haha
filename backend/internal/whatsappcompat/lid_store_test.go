package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualOriginalLIDUserDeviceMappingAndOldDiskFiles(t *testing.T) {
	actions := []map[string]any{{"op": "store", "pairs": []map[string]string{{"lid": "999@lid", "pn": "111@s.whatsapp.net"}, {"lid": "777:4@lid", "pn": "333:7@s.whatsapp.net"}, {"lid": "invalid@g.us", "pn": "111@s.whatsapp.net"}}}}
	for _, jid := range []string{"111@s.whatsapp.net", "111:1@s.whatsapp.net", "111:99@hosted", "111:0@hosted", "333:7@s.whatsapp.net", "missing@s.whatsapp.net", "111@g.us"} {
		actions = append(actions, map[string]any{"op": "lid", "jid": jid})
	}
	for _, jid := range []string{"999@lid", "999:8@lid", "999:99@hosted.lid", "777@lid", "missing@lid"} {
		actions = append(actions, map[string]any{"op": "pn", "jid": jid})
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_lid.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(nodeJSON(map[string]any{"actions": actions}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var want any
	if e = json.Unmarshal(data, &want); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	service := NodeLIDStore{Dir: dir}
	values := []any{}
	for _, action := range actions {
		switch action["op"] {
		case "store":
			pairs := [][2]string{}
			for _, p := range action["pairs"].([]map[string]string) {
				pairs = append(pairs, [2]string{p["lid"], p["pn"]})
			}
			if e := service.StoreLIDPNMappings(context.Background(), pairs); e != nil {
				t.Fatal(e)
			}
		case "lid":
			v, e := service.GetLIDForPN(context.Background(), action["jid"].(string))
			if e != nil {
				t.Fatal(e)
			}
			if v == "" {
				values = append(values, nil)
			} else {
				values = append(values, v)
			}
		case "pn":
			v, e := service.GetPNForLID(context.Background(), action["jid"].(string))
			if e != nil {
				t.Fatal(e)
			}
			if v == "" {
				values = append(values, nil)
			} else {
				values = append(values, v)
			}
		}
	}
	got := map[string]any{"values": values, "files": service.Snapshot()}
	if !bytes.Equal(nodeJSON(got), nodeJSON(want)) {
		t.Fatalf("Go%s Source%s", nodeJSON(got), nodeJSON(want))
	}
	cold := NodeLIDStore{Dir: dir}
	if v, e := cold.GetLIDForPN(context.Background(), "111:8@s.whatsapp.net"); e != nil || v != "999:8@lid" {
		t.Fatal("old mapping cold restore", v, e)
	}
}
