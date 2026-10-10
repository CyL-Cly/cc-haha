package modelyield

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
)

// The old coarse callback can synchronously wait on a captured Scope and
// cannot be invoked by an SDK generator reaction. It needs a true demand
// adapter before using this unique model consumer. RawStream's legacy exact
// cause tests remain unchanged; this guard does not claim legacy host parity.
func TestLegacyCoarseBodyOwnerRequiresFreshDemandAdapterBeforeHTTP(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { count.Add(1) }))
	defer server.Close()
	input, err := contextFromFixture(fixtureInput{Env: map[string]string{"ANTHROPIC_BASE_URL": server.URL, "ANTHROPIC_API_KEY": "private-key", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_DISABLE_FAST_MODE": "1"}, Model: "deepseek-flash", MaxOutputTokens: 32000, DeviceID: "fixture-device", SessionID: "00000000-0000-4000-8000-000000000000", Runtime: provider.SourceRuntime{Platform: "darwin", Arch: "arm64", Version: "v24.3.0"}})
	if err != nil {
		t.Fatal(err)
	}
	waits, emits := 0, 0
	outcome, err := Run(context.Background(), input, Dependencies{Request: requestretry.Dependencies{UUID: func() string { return "original-id" }, Now: time.Now, OpenIOAwait: func(provider.OwnerIOWait, func(context.Context) ([]byte, error)) ([]byte, error) {
		waits++
		return nil, errors.New("unadmitted legacy callback")
	}}, Emit: func(Yield) (requestretry.NoticeAction, error) { emits++; return requestretry.Resume, nil }})
	var capability *CapabilityError
	if !errors.As(err, &capability) || outcome.Reason != "" || emits != 0 || waits != 0 || count.Load() != 0 {
		t.Fatal("legacy body fallback or partial activation occurred", outcome, err, emits, waits, count.Load())
	}
}
