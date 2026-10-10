package adapterconfig

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

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestAdapterConfigLegacyMaskMergeAndAllPlatformSchemaActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	seed := json.RawMessage(`{"future":{"opaque":[1,true]},"telegram":{"botToken":"fake-long-token","allowedUsers":[7],"futureTelegram":true},"feishu":{"appSecret":"fake-other-secret","futureFeishu":"preserved"},"pairing":{"code":"ABC234","createdAt":123,"expiresAt":456}}`)
	rows := []map[string]any{{"seed": seed}, {"seed": seed, "patch": map[string]any{"telegram": map[string]any{"botToken": "****oken", "allowedUsers": []int{8}}, "pairing": map[string]any{"code": "******"}}}, {"seed": seed, "patch": map[string]any{"telegram": map[string]any{"botToken": "new-fake-secret", "defaultWorkDir": "/fake", "pairedUsers": []any{map[string]any{"userId": 7, "displayName": "Fixture", "pairedAt": 123}}}}}}
	for _, platform := range Platforms {
		rows = append(rows, map[string]any{"seed": seed, "patch": map[string]any{platform: map[string]any{"allowedUsers": []any{}, "pairedUsers": []any{}, "defaultWorkDir": " fixture ", "allowedProjectRoots": []string{" /tmp "}}}})
	}
	for _, patch := range []string{`null`, `[]`, `{"unknown":true}`, `{"telegram":{"botToken":null}}`, `{"telegram":{"allowedUsers":[0]}}`, `{"telegram":{"allowedUsers":[1.1]}}`, `{"telegram":{"allowedUsers":["7"]}}`, `{"telegram":{"pairedUsers":[{"userId":7,"displayName":"F","pairedAt":-1}]}}`, `{"telegram":{"future":true}}`, `{"pairing":{"code":3}}`, `{"pairing":{"expiresAt":-1}}`, `{"allowedProjectRoots":[" "]}`, `{"feishu":{"domain":"other"}}`, `{"feishu":{"streamingCard":1}}`, `{"slack":{"appToken":false}}`} {
		rows = append(rows, map[string]any{"seed": seed, "patch": json.RawMessage(patch)})
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-adapter-config-service.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_ADAPTER_CONFIG_SERVICE_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(adapterJSON(map[string]any{"rows": rows}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("original adapter config", err, stderr.String())
	}
	var wants []json.RawMessage
	if json.Unmarshal(out, &wants) != nil || len(wants) != len(rows) {
		t.Fatal(string(out))
	}
	private := t.TempDir()
	service, err := NewService(ServiceOptions{ConfigDir: private, Store: storage.New(storage.Options{}), LogError: func(error) {}})
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		if err = os.WriteFile(filepath.Join(private, "adapters.json"), adapterJSON(row["seed"]), 0600); err != nil {
			t.Fatal(err)
		}
		got := map[string]any{}
		var failure error
		if value, present := row["patch"]; present {
			patch, e := ParsePatch(adapterJSON(value))
			if e != nil {
				failure = e
			} else {
				got["patch"] = patch
				failure = service.Update(ctx, patch)
			}
		}
		if failure == nil {
			masked, e := service.MaskedJSON(ctx)
			if e != nil {
				failure = e
			} else {
				raw, e := service.ReadJSON(ctx)
				if e != nil {
					t.Fatal(e)
				}
				stored, e := os.ReadFile(filepath.Join(private, "adapters.json"))
				if e != nil {
					t.Fatal(e)
				}
				info, e := os.Stat(filepath.Join(private, "adapters.json"))
				if e != nil {
					t.Fatal(e)
				}
				got["masked"], got["raw"], got["mode"], got["stored"] = json.RawMessage(masked), json.RawMessage(raw), info.Mode().Perm(), string(stored)
			}
		}
		if failure != nil {
			api, ok := failure.(*Error)
			if !ok {
				t.Fatal(failure)
			}
			got = map[string]any{"error": api.Message, "code": api.Code, "status": api.StatusCode}
		}
		var actual, want any
		_ = json.Unmarshal(adapterJSON(got), &actual)
		_ = json.Unmarshal(wants[i], &want)
		if !reflect.DeepEqual(actual, want) {
			t.Errorf("row%d got%s want%s", i, adapterJSON(got), wants[i])
		}
	}
}
