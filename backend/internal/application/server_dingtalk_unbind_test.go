package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestDesktopDingTalkUnbindUsesRealSharedConfigAndRetainsOtherPlatformBindings(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	home, config := o.Environment["HOME"], o.Environment["CLAUDE_CONFIG_DIR"]
	file := filepath.Join(config, "adapters.json")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	seed := []byte(`{"future":{"keep":17},"telegram":{"botToken":"fake-retained-token","futureTelegram":true},"dingtalk":{"clientId":"fake-client","clientSecret":"fake-dingtalk-secret","permissionCardTemplateId":"fake-card","allowedUsers":["U1"],"pairedUsers":[{"userId":"U2","displayName":"old","pairedAt":1}],"defaultWorkDir":"/private/old","futureDingTalk":{"keep":true}}}`)
	if err := os.WriteFile(file, seed, 0600); err != nil {
		t.Fatal(err)
	}
	service, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: home, ConfigDir: config, WorkDir: home, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close(context.Background()) })
	peer := httptest.NewServer(service)
	t.Cleanup(peer.Close)
	request := func(method, path string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(method, peer.URL+path, nil)
		res, err := peer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("actual desktop %s %s: %d %s %v", method, path, res.StatusCode, data, err)
		}
		var result map[string]any
		if err = json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := request("GET", "/api/adapters")
	if before["dingtalk"].(map[string]any)["clientSecret"] != "****cret" {
		t.Fatal("actual old config credential masking", before)
	}
	unbound := request("POST", "/api/adapters/dingtalk/unbind")
	dingtalk := unbound["dingtalk"].(map[string]any)
	for _, key := range []string{"clientId", "clientSecret", "permissionCardTemplateId"} {
		if _, present := dingtalk[key]; present {
			t.Errorf("unbound account identity retained: %s", key)
		}
	}
	if dingtalk["defaultWorkDir"] != "/private/old" || !reflect.DeepEqual(dingtalk["futureDingTalk"], map[string]any{"keep": true}) || len(dingtalk["allowedUsers"].([]any)) != 0 || len(dingtalk["pairedUsers"].([]any)) != 0 {
		t.Fatal("unbind did not retain original scope/reset behavior", unbound)
	}
	if !reflect.DeepEqual(unbound["telegram"], before["telegram"]) || !reflect.DeepEqual(unbound["future"], before["future"]) {
		t.Fatal("unbind changed other platform or opaque future fields", unbound)
	}
	if after := request("GET", "/api/adapters"); !reflect.DeepEqual(after, unbound) {
		t.Fatal("shared reader returned stale unbound config", after, unbound)
	}
	stored, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("fake-dingtalk-secret")) || !bytes.Contains(stored, []byte("fake-retained-token")) {
		t.Fatal("actual persisted account fields did not match unbind")
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("persisted private config mode", info, err)
	}
	first := append([]byte(nil), stored...)
	request("POST", "/api/adapters/dingtalk/unbind")
	stored, err = os.ReadFile(file)
	if err != nil || !bytes.Equal(stored, first) {
		t.Fatal("repeated unbind changed the document", err)
	}
}
