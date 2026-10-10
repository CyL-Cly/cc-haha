package desktopoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func actualGrokCatalogSource(t *testing.T, input any) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-grok-model-catalog.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_GROK_CATALOG_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(encode(input))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("Source catalog %v %s", err, stderr.String())
	}
	return out
}

func TestGrokDesktopCatalogCacheMatchesActualSourceAndPhysicalRequests(t *testing.T) {
	const start = int64(1770000000000)
	token := func(account string) any {
		return map[string]any{"accessToken": "fake-" + account, "expiresAt": nil, "email": account}
	}
	reply := func(name string) any {
		return map[string]any{"models": []any{map[string]any{"id": name}}}
	}
	actions := []map[string]any{
		{"tokens": token("a"), "hold": true, "resolve": true}, {"resolve": true}, {"complete": true, "hold": false}, {"resolve": true},
		{"now": start + 300000, "hold": true, "reply": reply("remote-stale"), "resolve": true}, {"resolve": true}, {"complete": true, "hold": false}, {"resolve": true},
		{"now": start + 600000, "reply": map[string]any{"status": 503}, "resolve": true}, {"wait": true}, {"resolve": true},
		{"now": start + 659999, "tokens": token("b"), "resolve": true},
		{"now": start + 660000, "reply": reply("remote-b"), "resolve": true}, {"wait": true}, {"resolve": true},
		{"tokens": token("c"), "hold": true, "reply": reply("remote-c"), "resolve": true}, {"tokens": token("d"), "resolve": true}, {"complete": true, "hold": false},
		{"reply": reply("remote-d"), "resolve": true}, {"wait": true}, {"resolve": true},
		{"reply": map[string]any{"status": 503}, "force": true, "resolve": true}, {"resolve": true},
		{"reply": reply("forced-d"), "force": true, "resolve": true}, {"resolve": true},
		{"tokens": nil, "force": true, "resolve": true}, {"tokens": token("d"), "resolve": true},
		{"tokens": true, "force": true, "resolve": true}, {"tokens": map[string]any{"expiresAt": nil}, "force": true, "resolve": true},
	}
	var source struct{ Cache, Requests []json.RawMessage }
	_ = json.Unmarshal(actualGrokCatalogSource(t, map[string]any{"now": start, "actions": actions}), &source)
	var now atomic.Int64
	now.Store(start)
	var mu sync.Mutex
	response := encode(reply("remote-a"))
	held := false
	var pending chan struct{}
	requests := []any{}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		body := append([]byte{}, response...)
		gate := pending
		shouldHold := held
		requests = append(requests, map[string]any{"authorization": r.Header.Get("Authorization")})
		mu.Unlock()
		if shouldHold {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		var reply struct{ Status int }
		_ = json.Unmarshal(body, &reply)
		if reply.Status > 0 {
			w.WriteHeader(reply.Status)
		}
		_, _ = w.Write(body)
	}))
	defer host.Close()
	home := t.TempDir()
	parent, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Platform: "linux", Environment: func() map[string]string { return map[string]string{} }, Now: func() time.Time { return time.UnixMilli(now.Load()) }, HTTPClient: &http.Client{Transport: openAITestTransport{host.URL, http.DefaultTransport}}})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	service := parent.Grok()
	got := []any{}
	wait := func(settled bool) {
		t.Helper()
		end := time.Now().Add(3 * time.Second)
		for time.Now().Before(end) {
			service.catalog.mu.Lock()
			flight := service.catalog.inFlight
			service.catalog.mu.Unlock()
			mu.Lock()
			count := len(requests)
			mu.Unlock()
			if settled && !flight || !settled && count > 0 {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("physical catalog did not settle")
	}
	for _, action := range actions {
		if n, ok := action["now"]; ok {
			now.Store(n.(int64))
		}
		mu.Lock()
		if r, ok := action["reply"]; ok {
			response = encode(r)
		}
		if h, ok := action["hold"]; ok {
			held = h.(bool)
			if held {
				pending = make(chan struct{})
			}
		}
		if action["complete"] == true {
			close(pending)
		}
		mu.Unlock()
		if value, present := action["tokens"]; present {
			if value == nil {
				err = service.DeleteTokens(context.Background())
			} else {
				err = os.MkdirAll(filepath.Dir(service.TokenPath()), 0700)
				if err == nil {
					err = os.WriteFile(service.TokenPath(), encode(value), 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if action["complete"] == true || action["wait"] == true {
			wait(true)
		}
		if action["resolve"] == true {
			models, e := service.ModelCatalog(context.Background(), action["force"] == true)
			if e != nil {
				t.Fatal(e)
			}
			// JS starts fetch synchronously before returning. Await only the
			// physical request admission, never its held upstream completion.
			service.catalog.mu.Lock()
			flight := service.catalog.inFlight
			service.catalog.mu.Unlock()
			if flight {
				end := time.Now().Add(3 * time.Second)
				for {
					mu.Lock()
					count := len(requests)
					mu.Unlock()
					if count >= len(grokExpectedRequests(source.Cache, len(got))) {
						break
					}
					if time.Now().After(end) {
						t.Fatal("request not admitted")
					}
					time.Sleep(time.Millisecond)
				}
			}
			values := []string{}
			for _, model := range models {
				values = append(values, model.Value)
			}
			mu.Lock()
			count := len(requests)
			mu.Unlock()
			got = append(got, map[string]any{"models": values, "calls": count})
		}
	}
	var expected, actual any
	_ = json.Unmarshal(encode(source.Cache), &expected)
	_ = json.Unmarshal(encode(got), &actual)
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("cache actual%s want%s", encode(got), encode(source.Cache))
	}
	mu.Lock()
	data := encode(requests)
	mu.Unlock()
	if !bytes.Equal(data, encode(source.Requests)) {
		t.Errorf("physical account requests%s want%s", data, encode(source.Requests))
	}
}

func grokExpectedRequests(rows []json.RawMessage, index int) []int {
	var row struct{ Calls int }
	_ = json.Unmarshal(rows[index], &row)
	return make([]int, row.Calls)
}
