package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestOrdinaryCLISettingsAndAPIKeyDoNotReadDesktopManagedOAuth(t *testing.T) {
	var wire []byte
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "fake-only-loopback" || r.Header.Get("Authorization") != "" {
			t.Error("ordinary CLI credential slot changed")
		}
		wire, _ = io.ReadAll(r.Body)
		response(w, "actual-cli-isolation", "", nil, "CLI_OWN_AUTH_DONE")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(filepath.Join(config, "cc-haha"), 0700); err != nil {
		t.Fatal(err)
	}
	tokens := []byte(`{"accessToken":"fake-desktop-must-not-be-used","refreshToken":"fake-desktop-refresh","expiresAt":0,"subscriptionType":"max","future":{"old":true}}`)
	tokenPath := filepath.Join(config, "cc-haha", "oauth.json")
	if err := os.WriteFile(tokenPath, tokens, 0600); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{"model":"fixture-from-original-user","future":{"keep":true}}`)
	settingsPath := filepath.Join(config, "settings.json")
	if err := os.WriteFile(settingsPath, settings, 0600); err != nil {
		t.Fatal(err)
	}
	o.Arguments.Model = ""
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(context.Background(), tui.Input{Text: "Complete using the original CLI credentials."}, nil); err != nil {
		t.Fatal(err)
	}
	if obj(wire).String("model") != "fixture-from-original-user" {
		t.Fatal("CLI settings model was replaced by desktop subscription", obj(wire).String("model"))
	}
	after, err := os.ReadFile(tokenPath)
	if err != nil || !bytes.Equal(tokens, after) {
		t.Fatal("ordinary CLI touched desktop credential JSON", err)
	}
	after, err = os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(settings, after) {
		t.Fatal("ordinary CLI rewrote existing user settings", err)
	}
	if s.desktopOAuthToken != nil {
		t.Fatal("ordinary CLI gained a desktop refresh callback")
	}
}
