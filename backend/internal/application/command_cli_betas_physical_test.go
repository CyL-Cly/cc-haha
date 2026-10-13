package application

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkbetas"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPublicCLIBetasReachInitialOutputRequestHeadersContextAndColdResume(t *testing.T) {
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		calls.Add(1)
		if !strings.Contains(r.Header.Get("Anthropic-Beta"), sdkbetas.Context1M) || bytes.Contains(data, []byte("bad-beta")) {
			t.Error("filtered public beta not used by actual request")
		}
		response(w, "cli-beta-done", "", nil, "BETA_PRIVATE_TASK_FINISHED")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	for _, cold := range []bool{false, true} {
		flag := "--session-id"
		if cold {
			flag = "--resume"
		}
		argv := []string{"--print", "--model", "claude-sonnet-4-6-private", "--tools", "Read", "--thinking", "disabled", flag, o.Arguments.SessionID, "--betas", "bad-beta", sdkbetas.Context1M, sdkbetas.Context1M, "--", "Complete private beta task"}
		command, _, err := ParseCommand(argv, o.Version, o.WorkDir, t.TempDir(), o.Environment)
		if err != nil {
			t.Fatal(err)
		}
		o.Arguments = command.Arguments
		var init protocol.JSONObject
		o.OnSDK = func(data json.RawMessage) error {
			record := obj(data)
			if record.String("subtype") == "init" {
				init = record
			}
			return nil
		}
		session, err := NewSession(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		if err = session.Submit(context.Background(), tui.Input{Text: command.Prompt}, nil); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(init.Get("betas"), raw([]string{sdkbetas.Context1M, sdkbetas.Context1M})) {
			t.Fatal("init lost actual filtered SDK beta state", string(init.Get("betas")))
		}
		var window float64
		if err = session.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			var err error
			window, err = session.process.Host.InferenceContextWindow(scope)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if window != 1000000 {
			t.Fatal("context did not share beta producer", window)
		}
		if err = session.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("beta startup/context/cold resume added hidden model requests", calls.Load())
	}
}
