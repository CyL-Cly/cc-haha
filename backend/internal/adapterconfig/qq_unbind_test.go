package adapterconfig

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestUnbindQQRemovesOnlyAccountFieldsThroughPrivateAtomicWriter(t *testing.T) {
	private := t.TempDir()
	file := filepath.Join(private, "adapters.json")
	seed := []byte(`{"future":{"keep":true},"dingtalk":{"clientSecret":"other-private"},"qq":{"appId":"id","appSecret":"secret","allowedUsers":["old"],"pairedUsers":[{"id":"old"}],"workspace":"keep","future":17}}`)
	if e := os.WriteFile(file, seed, 0600); e != nil {
		t.Fatal(e)
	}
	service, e := NewService(ServiceOptions{ConfigDir: private, Store: storage.New(storage.Options{}), LogError: func(error) {}})
	if e != nil {
		t.Fatal(e)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if e := service.UnbindQQ(cancelled); e == nil {
		t.Fatal("cancelled write succeeded")
	}
	raw, e := os.ReadFile(file)
	if e != nil || string(raw) != string(seed) {
		t.Fatal("cancelled write changed original document", e)
	}
	for i := 0; i < 2; i++ {
		if e := service.UnbindQQ(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	raw, e = os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	var result map[string]any
	if e = json.Unmarshal(raw, &result); e != nil {
		t.Fatal(e)
	}
	section := result["qq"].(map[string]any)
	for _, key := range []string{"appId", "appSecret"} {
		if _, ok := section[key]; ok {
			t.Fatal("account field retained", key)
		}
	}
	if section["workspace"] != "keep" || section["future"] != float64(17) || result["dingtalk"].(map[string]any)["clientSecret"] != "other-private" || result["future"].(map[string]any)["keep"] != true || len(section["allowedUsers"].([]any)) != 0 || len(section["pairedUsers"].([]any)) != 0 {
		t.Fatal("unrelated or paired state corrupted", result)
	}
	info, e := os.Stat(file)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("atomic mode", e)
	}
	files, e := filepath.Glob(file + ".tmp.*")
	if e != nil || len(files) != 0 {
		t.Fatal("temporary file leak", files, e)
	}
}
