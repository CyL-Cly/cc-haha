package providerconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestClaudeOfficialDefaultAndDeletedProviderAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source comparison requires pinned Bun")
	}
	service, config := fixture(t)
	home := filepath.Dir(config)
	rows := []map[string]any{}
	for _, subscription := range []string{"max", "pro", "", "enterprise"} {
		for _, configured := range []any{nil, "", "opus[1m]", "Opus:1M", "sonnet", "fable", "haiku", "Claude-older:1m", "third-party", "\ufeff sonnet \u00a0", 7, false} {
			rows = append(rows, map[string]any{"tokens": map[string]any{"accessToken": "fake-managed", "refreshToken": "fake-refresh", "expiresAt": nil, "subscriptionType": subscription, "future": map[string]any{"keep": true}}, "user": map[string]any{"model": configured, "modelContext": "1m", "effort": " high "}, "configured": configured})
		}
	}
	for _, user := range []any{map[string]any{}, map[string]any{"model": "api-key-model", "modelContext": "1m"}} {
		rows = append(rows, map[string]any{"tokens": nil, "user": user, "configured": "sonnet"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	script, _ := filepath.Abs("../../../testdata/native-claude-official-runtime.fixture.ts")
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", script)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=" + filepath.Dir(bun) + ":/usr/bin:/bin", "NODE_ENV=production", "DISABLE_TELEMETRY=1"}
	data, _ := json.Marshal(map[string]any{"rows": rows})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source: %v %s", err, stderr.String())
	}
	var source struct {
		Observations []struct {
			Model, Effort, DefaultModel, RestoredModel, RestoredInput string
			Resolved                                                  *string
			Replacement                                               any
			ProviderError                                             struct {
				Status        int
				Code, Message string
			}
		}
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Observations) != len(rows) || len(source.Capabilities.Blocked) != 0 {
		t.Fatalf("Source observation: %v %s", err, output)
	}
	oauth, err := desktopoauth.New(desktopoauth.Options{Home: home, ConfigDir: config, Platform: "linux", Store: service.options.Store, Environment: func() map[string]string { return map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": config} }})
	if err != nil {
		t.Fatal(err)
	}
	defer oauth.Close()
	service.options.ClaudeRuntimeModel = oauth.ResolveRuntimeModel
	for i, row := range rows {
		seed(t, service.indexPath(), `{"schemaVersion":6,"activeId":null,"providers":[]}`)
		seed(t, filepath.Join(config, "cc-haha", "settings.json"), `{}`)
		user, _ := json.Marshal(row["user"])
		seed(t, filepath.Join(config, "settings.json"), string(user))
		if row["tokens"] == nil {
			_ = os.Remove(oauth.TokenPath())
		} else {
			value, _ := json.Marshal(row["tokens"])
			seed(t, oauth.TokenPath(), string(value))
		}
		configured, _ := json.Marshal(row["configured"])
		resolved, err := oauth.ResolveRuntimeModel(ctx, configured)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(resolved, source.Observations[i].Resolved) {
			t.Errorf("row %d resolver Source %v Go %v", i, source.Observations[i].Resolved, resolved)
		}

		initial, err := ResolveDesktopRuntimeSelection(ctx, service.options, nil)
		if err != nil || initial.ModelID != source.Observations[i].DefaultModel {
			t.Errorf("row %d default startup Source %q Go %+v %v", i, source.Observations[i].DefaultModel, initial, err)
		}
		restored, err := ResolveDesktopRuntimeSelection(ctx, service.options, &Selection{ModelID: source.Observations[i].RestoredInput, EffortLevel: "high"})
		if err != nil || restored.ModelID != source.Observations[i].RestoredModel || restored.EffortLevel != "high" {
			t.Errorf("row %d restored startup Source %q Go %+v %v", i, source.Observations[i].RestoredModel, restored, err)
		}
		// The actual default producer is reached by a stale saved-provider request.
		input := storage.Object{"providerId": encode("deleted"), "modelId": encode(" stale "), "effortLevel": encode("high")}
		actual, err := NormalizeRuntimeSelection(ctx, service.options, input)
		if err != nil {
			t.Errorf("row %d actual default producer: %v", i, err)
			continue
		}
		if actual.ModelID != source.Observations[i].Model || actual.EffortLevel != source.Observations[i].Effort {
			t.Errorf("row %d Source model=%q effort=%q Go %+v", i, source.Observations[i].Model, source.Observations[i].Effort, actual)
		}
		_, err = service.Provider(ctx, "claude-official")
		var api *settings.APIError
		if !errors.As(err, &api) || api.StatusCode != source.Observations[i].ProviderError.Status || api.Message != source.Observations[i].ProviderError.Message {
			t.Errorf("row %d provider error Source %+v Go %v", i, source.Observations[i].ProviderError, err)
		}
	}
}
