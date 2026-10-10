package requestretry

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestNativeModelValidationObserverSeesActualNonstreamBodyBeforeOwnedSend(t *testing.T) {
	var actual []byte
	var mu sync.Mutex
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		actual = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"owned-json","content":[],"usage":{}}`))
	}))
	defer peer.Close()
	owner := cooperative.New()
	defer owner.Close()
	var observed json.RawMessage
	calls := 0
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		authority, err := cooperative.NewNativeModelValidationModels(scope, owner)
		if err != nil {
			return err
		}
		input, deps := standardInput(), standardDeps()
		input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
		input.Context.Host.Env["ANTHROPIC_BASE_URL"] = peer.URL
		input.Context.Options.Model = "Actual-Custom"
		input.Context.Options.QuerySource = "model_validation"
		input.Context.Host.DeviceID = "owned-device"
		input.Context.Host.SessionID = "owned-session"
		deps.StreamOwner, deps.StreamScope = owner, scope
		deps.NativeOpenIO = providernative.NewFactory(owner, scope, nil)
		deps.BodyIOAwait = func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
			return scope.AwaitBytes(cooperative.StreamNext, work)
		}
		deps.ObserveRequest = func(_ string, _ http.Header, body json.RawMessage) {
			calls++
			observed = append(json.RawMessage(nil), body...)
			mu.Lock()
			defer mu.Unlock()
			if actual != nil {
				t.Error("observer ran after physical request")
			}
		}
		return ValidateNativeModel(context.Background(), input, protocol.NewJSONObject(), deps, authority)
	})
	if err != nil {
		t.Fatal("real nonstream request observer failed", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 || !bytes.Equal(actual, observed) || bytes.Contains(observed, []byte(`"stream":true`)) || !bytes.Contains(observed, []byte(`"max_tokens":1`)) {
		t.Fatal("actual nonstream observer/body", calls, string(observed), string(actual))
	}
}
