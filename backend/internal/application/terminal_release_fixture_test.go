package application

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Updater consumers intentionally allow nonessential traffic. Keep that real
// gate while selecting the original release-note startup's warm-version branch.
func seedTerminalReleaseWarmFixture(t *testing.T, o Options) {
	t.Helper()
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	path := filepath.Join(config, ".claude.json")
	global := protocol.NewJSONObject()
	data, err := os.ReadFile(path)
	if err == nil {
		if err = global.UnmarshalJSON(data); err != nil {
			t.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	global.Set("lastReleaseNotesSeen", raw(o.Version))
	data, err = global.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(config, "cache"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config, "cache", "changelog.md"), []byte("## 0.0.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// These consumers use explicit HTTP loopback installer peers. Every HTTPS
	// CONNECT is unowned and must fail before an external socket is opened.
	var requests atomic.Int32
	guard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("updater fixture attempted unowned HTTP", r.Method, r.Host, r.URL)
		w.WriteHeader(http.StatusForbidden)
	}))
	for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
		// The updater's nonessential-traffic gate stays open. Its fixture does
		// not own the separate first-party analytics exporter, which otherwise
		// flushes to api.anthropic.com when this real Session closes.
		env["DISABLE_TELEMETRY"] = "1"
		if env["HTTPS_PROXY"] != "" {
			t.Fatal("updater fixture unexpectedly owns another HTTPS proxy")
		}
		env["HTTPS_PROXY"] = guard.URL
	}
	t.Cleanup(func() {
		guard.Close()
		if requests.Load() != 0 {
			t.Fatal("warm updater fixture attempted public HTTP", requests.Load())
		}
		t.Log("actual updater startup private CONNECT guard: zero external attempts")
	})
}
