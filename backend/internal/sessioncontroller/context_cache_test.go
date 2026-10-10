package sessioncontroller

import (
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// The expected date is the actual production fetchSystemPromptParts/custom
// prompt + prependUserContext observation across midnight, not a Go recipe.
func TestPrepareKeepsActualSourceUserContextDateAcrossMidnight(t *testing.T) {
	clock := time.Date(2026, 10, 1, 23, 59, 59, 0, time.UTC)
	serial := 0
	uuid := func() string { serial++; return strings.Repeat("a", serial) }
	now := func() time.Time { return clock }
	factory, err := messages.NewFactory(uuid, now)
	if err != nil {
		t.Fatal(err)
	}
	options := hostOptions(t, t.TempDir()).options
	options.Now = now
	options.UUID = uuid
	options.Factory = factory
	options.WriterOptions.SessionPersistenceDisabled = true
	controller, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.Activate(activation()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(controller.AwaitBackground)

	input := requestcontext.Context{Thinking: requestcontext.Thinking{Type: "disabled"}, Options: requestcontext.Options{Model: "fixture-model"}, Host: requestcontext.Host{Profile: requestcontext.DirectRelayProfile, Env: map[string]string{"ANTHROPIC_API_KEY": "fixture", "ANTHROPIC_BASE_URL": "http://127.0.0.1:1"}, DeviceID: "fixture-device"}}
	for turn := 0; turn < 2; turn++ {
		prepared, err := controller.Prepare(input, requestcontext.Dependencies{UUID: uuid, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		request, err := prepared.BuildAttempt(requestcontext.AttemptOptions{Model: "fixture-model"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(request.PreparedRequest.Body), "Today's date is 2026-10-01.") {
			t.Fatalf("Source memoized date changed on turn %d: %s", turn, request.PreparedRequest.Body)
		}
		clock = clock.Add(2 * time.Second)
	}
}
