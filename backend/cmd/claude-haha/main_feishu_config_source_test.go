package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
)

func TestActualSourceFeishuPublicFactoryRawCredentialsDomainAndEventKeys(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	type profile struct {
		File        string            `json:"file"`
		Environment map[string]string `json:"env"`
	}
	profiles := []profile{}
	for _, v := range []string{`"file"`, `""`, `null`, `false`, `true`, `0`, `7`, `[]`, `{"raw":true}`, `"raw-\ud800-\udfff"`} {
		profiles = append(profiles, profile{`{"feishu":{"appId":` + v + `,"appSecret":` + v + `,"domain":` + v + `,"encryptKey":"file-key","verificationToken":"file-token"}}`, map[string]string{}})
	}
	for _, v := range []string{"", "lark", "LARK", "feishu", "  ", "0"} {
		profiles = append(profiles, profile{`{"feishu":{"appId":"file-id","appSecret":"file-secret","domain":"lark","encryptKey":"file-key","verificationToken":"file-token"}}`, map[string]string{"FEISHU_APP_ID": v, "FEISHU_APP_SECRET": v, "FEISHU_DOMAIN": v, "FEISHU_ENCRYPT_KEY": v, "FEISHU_VERIFICATION_TOKEN": v}})
	}
	profiles = append(profiles, profile{`{"feishu":{"appId":"file-id","appSecret":"file-secret","domain":"\u006cark","encryptKey":"raw-\ud800-\udfff","verificationToken":"v"}}`, map[string]string{}})
	fixture, _ := filepath.Abs("../../testdata/native-feishu-factory-config.fixture.ts")
	input, _ := json.Marshal(map[string]any{"profiles": profiles})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_FEISHU_FACTORY_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	wanted, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var expected []map[string]json.RawMessage
	if err = json.Unmarshal(wanted, &expected); err != nil || len(expected) != len(profiles) {
		t.Fatal("actual Source shape", err)
	}
	previousClient, previousRuntime := newNativeFeishuClient, newNativeFeishuRuntime
	defer func() { newNativeFeishuClient, newNativeFeishuRuntime = previousClient, previousRuntime }()
	stop := errors.New("owned factory observation before startup")
	for i, p := range profiles {
		if err = os.WriteFile(filepath.Join(config, "adapters.json"), []byte(p.File), 0600); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config}
		for k, v := range p.Environment {
			env[k] = v
		}
		var got map[string]json.RawMessage
		newNativeFeishuClient = func(ctx context.Context, o imruntime.FeishuClientOptions) (*imruntime.FeishuClient, error) {
			got = map[string]json.RawMessage{"appId": o.Credentials.Get("appId"), "appSecret": o.Credentials.Get("appSecret")}
			got["domain"], _ = json.Marshal(o.Domain)
			return imruntime.NewFeishuClient(ctx, o)
		}
		newNativeFeishuRuntime = func(ctx context.Context, o imruntime.FeishuRuntimeOptions) (*imruntime.FeishuRuntime, error) {
			got["encryptKey"], _ = o.EncryptKey.MarshalJSON()
			got["verificationToken"], _ = o.VerificationToken.MarshalJSON()
			return nil, stop
		}
		process, e := startNativeFeishu(context.Background(), adapterlauncher.Options{HomeDir: home, WorkDir: home, Environment: env, Stderr: io.Discard})
		if process != nil || !errors.Is(e, stop) {
			t.Fatal("factory observation", e)
		}
		for k, w := range expected[i] {
			if len(w) > 0 && w[0] == '"' {
				a, e1 := protocol.DecodeJavaScriptJSONString(got[k])
				b, e2 := protocol.DecodeJavaScriptJSONString(w)
				if e1 != nil || e2 != nil || !reflect.DeepEqual(a, b) {
					t.Fatalf("Source UTF16 profile%d.%s got%s want%s", i, k, got[k], w)
				}
				continue
			}
			var a, b any
			_ = json.Unmarshal(got[k], &a)
			_ = json.Unmarshal(w, &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("Source profile%d.%s got%s want%s", i, k, got[k], w)
			}
		}
	}
}
