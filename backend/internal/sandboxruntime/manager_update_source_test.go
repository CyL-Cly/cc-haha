package sandboxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualSourceManagerUpdateDeepCloneKeepsPolicyImmutable(t *testing.T) {
	home := t.TempDir()
	input := testObject(t, `{"network":{"allowedDomains":["original.example"],"deniedDomains":[],"allowUnixSockets":["original"],"allowLocalBinding":true},"filesystem":{"allowWrite":["original"],"denyRead":[],"allowRead":[],"denyWrite":[]},"ignoreViolations":{"*":["original"]},"ripgrep":{"command":"rg","args":["original"]},"enableWeakerNetworkIsolation":true}`)
	raw, _ := input.MarshalJSON()
	fixture, _ := filepath.Abs("testdata/manager-update.fixture.ts")
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(append(append([]byte(`{"config":`), raw...), '}'))
	source, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(source))
	}
	var config Config
	if e = json.Unmarshal(raw, &config); e != nil {
		t.Fatal(e)
	}
	network, e := NewNetwork(context.Background(), config.Network, NetworkOptions{})
	if e != nil {
		t.Fatal(e)
	}
	defer network.Close(context.Background())
	m := &Manager{network: network}
	m.Update(ConvertedConfig{Config: config})
	config.Network.AllowedDomains = append(config.Network.AllowedDomains, "injected.example")
	(*config.Network.AllowUnixSockets)[0] = "changed"
	*config.Network.AllowLocalBinding = false
	config.Filesystem.AllowWrite[0] = "changed"
	config.IgnoreViolations = []byte(`{"*":["original","changed"]}`)
	config.Ripgrep.Set("args", []byte(`["original","changed"]`))
	*config.EnableWeakerNetworkIsolation = false
	actual, _ := json.Marshal(m.config)
	var one, two any
	json.Unmarshal(source, &one)
	json.Unmarshal(actual, &two)
	if !reflect.DeepEqual(one, two) {
		t.Fatalf("Source policy %s; native %s", source, actual)
	}
	if !*network.config.AllowLocalBinding || (*network.config.AllowUnixSockets)[0] != "original" {
		t.Fatal("live network config borrowed mutable caller")
	}
}
