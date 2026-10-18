package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func openAIAuthMainFixture(t *testing.T) *MainPrint {
	return openAIAuthMainFixtureWithEnv(t, nil)
}
func openAIAuthMainFixtureWithEnv(t *testing.T, extra map[string]string) *MainPrint {
	return openAIAuthMainFixtureWithOptions(t, extra, nil, nil)
}
func openAIAuthMainFixtureWithOptions(t *testing.T, extra map[string]string, setup func(*NativeBoundary), configure func(*MainPrintOptions)) *MainPrint {
	t.Helper()
	f := newNativePluginFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-task-fake-key"
	f.n.Environment["CLAUDE_CODE_TMPDIR"] = f.n.Temp
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	for key, value := range extra {
		f.n.Environment[key] = value
	}
	// Never invoke the machine Keychain, even for a private service identity.
	f.n.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("isolated Keychain unavailable")
	}
	if setup != nil {
		setup(f.n)
	}
	disabled := false
	options := MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "No inference in OAuth lifecycle proof", Tools: []string{"Read"}, MaxTurns: 1, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard}
	if configure != nil {
		configure(&options)
	}
	m, err := NewMainPrint(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.StartSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.ShutdownSession(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return m
}

func TestNativeOpenAIAuthSameOwnerFileRefreshPhysicalHTTPAtomicSaveAndSharedSecure(t *testing.T) {
	m := openAIAuthMainFixture(t)
	ctx := context.Background()
	now := int64(m.options.Native.NowMillis())
	path := filepath.Join(m.options.Native.Home, "private-openai.json")
	if err := os.WriteFile(path, rawMain(map[string]any{"accessToken": "private-fake-old", "refreshToken": "private-fake-refresh", "expiresAt": now - 1, "email": "private@invalid", "clientId": "old-client", "future": true}), 0600); err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || string(body) != "grant_type=refresh_token&refresh_token=private-fake-refresh&client_id="+desktopoauth.OpenAICodexClientID+"&scope=openid+profile+email" {
			t.Errorf("actual refresh body: %s", body)
		}
		if r.Header.Get("User-Agent") != desktopoauth.OpenAICodexTokenUserAgent {
			t.Error("refresh agent")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"private-fake-new","expires_in":3600}`)
	}))
	defer peer.Close()
	binding, err := m.NewNativeOpenAIAuth(func(ctx context.Context, scope *cooperative.Scope, token json.RawMessage) (protocol.JSONObject, error) {
		request, err := desktopoauth.OpenAIRefreshRequest(token)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		future, err := nativeSkillWork(m, scope, func() ([]byte, error) {
			r, err := http.NewRequestWithContext(ctx, request.Method, peer.URL, strings.NewReader(request.Body))
			if err != nil {
				return nil, err
			}
			for _, h := range request.Headers {
				r.Header.Set(h[0], h[1])
			}
			response, err := http.DefaultClient.Do(r)
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()
			data, readErr := io.ReadAll(response.Body)
			return desktopoauth.DecodeOpenAITokenResponse(response.StatusCode, data, readErr)
		})
		if err != nil {
			return protocol.JSONObject{}, err
		}
		data, err := future.Await(scope)
		if err != nil {
			return protocol.JSONObject{}, nativeOpenAIAuthOwnerFailure(err)
		}
		var response protocol.JSONObject
		err = response.UnmarshalJSON(data)
		return response, err
	})
	if err != nil {
		t.Fatal(err)
	}
	var first, cached, fresh *openaiauth.Value
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.environment.SetProperty(quoteMain(openaiauth.FileEnvironmentKey), quoteMain(path)); err != nil {
			return err
		}
		var err error
		first, err = binding.GetCached(ctx, scope)
		if err != nil {
			return err
		}
		cached, err = binding.GetCached(ctx, scope)
		if err != nil {
			return err
		}
		fresh, err = binding.EnsureFresh(ctx, scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if first == nil || first != cached || fresh == nil || fresh.Object.String("accessToken") != "private-fake-new" || fresh.Object.String("refreshToken") != "private-fake-refresh" || fresh.Object.String("email") != "private@invalid" || posts.Load() != 1 {
		t.Fatalf("actual same-owner auth: first%v cachedSame%v fresh%v posts%d", first, first == cached, fresh, posts.Load())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 || !strings.Contains(string(data), "\"email\": \"private@invalid\",\n  \"clientId\"") {
		t.Fatalf("physical file order/mode: %o %s", info.Mode().Perm(), data)
	}
	tmp, err := filepath.Glob(path + ".tmp.*")
	if err != nil || len(tmp) != 0 {
		t.Fatal("atomic temp leak", tmp, err)
	}
	marker := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "cc-haha", openaiauth.MarkerFile)
	if data, err = os.ReadFile(marker); err != nil || string(data) != "1\n" {
		t.Fatal("actual marker", string(data), err)
	}
	// Same shared secure producer, preserving the existing account and future
	// fields; a successful secure save removes the stale-fallback marker.
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if err := m.environment.SetProperty(quoteMain(openaiauth.FileEnvironmentKey), nil); err != nil {
			return err
		}
		initial := protocol.NewJSONObject(protocol.JSONField{Name: "future", Value: rawMain(map[string]any{"keep": true})}, protocol.JSONField{Name: "claudeAiOauth", Value: rawMain(map[string]any{"accessToken": "unrelated-fake"})})
		if !m.secure.Update(ctx, initial).Success {
			return errors.New("shared secure initial update failed")
		}
		result, err := binding.Save(ctx, scope, fresh.Object)
		if err != nil {
			return err
		}
		if !result.Success {
			return errors.New("shared secure OAuth update failed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("marker not removed", err)
	}
	stored, err := m.secure.Read(ctx)
	if err != nil || stored.Get("future") == nil || stored.Get("claudeAiOauth") == nil || stored.Get(openaiauth.StorageKey) == nil {
		t.Fatal("shared secure state lost", stored, err)
	}
}

func TestNativeOpenAIAuthForeignOwnerAndOrdinaryRefreshFailure(t *testing.T) {
	m := openAIAuthMainFixture(t)
	ctx := context.Background()
	calls := 0
	binding, err := m.NewNativeOpenAIAuth(func(context.Context, *cooperative.Scope, json.RawMessage) (protocol.JSONObject, error) {
		calls++
		return protocol.JSONObject{}, context.DeadlineExceeded
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(ctx, func(scope *cooperative.Scope) error {
		_, err := binding.EnsureFresh(ctx, scope)
		if !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign owner accepted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("foreign scope had effect")
	}
	var result *openaiauth.Value
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		o := protocol.NewJSONObject(protocol.JSONField{Name: "accessToken", Value: quoteMain("fake-old")}, protocol.JSONField{Name: "refreshToken", Value: quoteMain("fake-refresh")}, protocol.JSONField{Name: "expiresAt", Value: rawMain(0)})
		stored, e := binding.Save(ctx, scope, o)
		if e != nil {
			return e
		}
		if !stored.Success {
			return errors.New("fixture secure save failed")
		}
		result, e = binding.EnsureFresh(ctx, scope)
		return e
	}); err != nil {
		t.Fatal("ordinary token timeout became administrative failure", err)
	}
	if result != nil || calls != 1 {
		t.Fatal("failed refresh returned credentials", result, calls)
	}
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		bad, e := m.NewNativeOpenAIAuth(func(context.Context, *cooperative.Scope, json.RawMessage) (protocol.JSONObject, error) {
			return protocol.JSONObject{}, &NativeOpenAIAuthAdministrativeError{cooperative.ErrClosed}
		})
		if e != nil {
			return e
		}
		_, e = bad.EnsureFresh(ctx, scope)
		if !errors.Is(e, cooperative.ErrClosed) {
			return fmt.Errorf("owner failure hidden: %v", e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
