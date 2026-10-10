package modelyield

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

type streamAbortInput struct {
	modelFixtureInput
	AbortType  string
	AbortIndex int
}
type streamAbortObservation struct {
	Yielded          []string           `json:"yielded"`
	YieldedAfter     []string           `json:"yieldedAfter"`
	Requests         []fetchObservation `json:"requests"`
	UUIDCalls        int                `json:"uuidCalls"`
	DateCalls        int                `json:"dateCalls"`
	PreparationCalls int                `json:"preparationCalls"`
	HistoryBefore    []string           `json:"historyBefore"`
	HistoryAfter     []string           `json:"historyAfter"`
	Returned         bool               `json:"returned"`
	Aborted          bool               `json:"aborted"`
}

func TestStreamAbortFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_STREAM_ABORT_FIXTURE") == "" {
		t.Skip("actual private HTTPS cancellation differential child")
	}
	var input streamAbortInput
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	if err := validateModelFixture(input.modelFixtureInput); err != nil {
		t.Fatal(err)
	}
	expectedType, expectedIndex := "content_block_stop", 0
	switch input.Name {
	case "completed-text":
	case "unfinished-text":
		expectedType = "content_block_delta"
	case "pending-local-tool":
		expectedIndex = 1
	case "server-boundary":
		expectedType = "content_block_delta"
		expectedIndex = 2
	default:
		t.Fatal("stream abort requires original control profile")
	}
	if input.Mode != "caller-abort" || input.Layer != "query" || input.AbortType != expectedType || input.AbortIndex != expectedIndex || input.Env["CLAUDE_CODE_SIMPLE"] != "1" || input.Env["CLAUDE_CODE_MAX_RETRIES"] != "0" || input.Env["CLAUDE_CODE_DISABLE_FAST_MODE"] != "1" {
		t.Fatal("stream abort requires original bare direct-key control")
	}
	original, err := contextFromFixture(input.fixtureInput)
	if err != nil {
		t.Fatal(err)
	}
	observation := streamAbortObservation{Yielded: []string{}, YieldedAfter: []string{}, Requests: []fetchObservation{}, HistoryBefore: append([]string{}, input.HistorySources...), HistoryAfter: []string{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live := []func() ([]byte, error){}
	tlsState, err := tlsconfig.NewState(os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Run(ctx, original, Dependencies{Request: requestretry.Dependencies{TLSState: tlsState,
		UUID: func() string { observation.UUIDCalls++; return fmt.Sprintf("abort-id-%d", observation.UUIDCalls) }, Now: func() time.Time { observation.DateCalls++; return time.UnixMilli(1700000000000) }, Random: func() float64 { return 0 }, Sleep: func(context.Context, float64) error { return errors.New("aborted response retried") },
		OnPreparation: func([]protocol.JSONObject) { observation.PreparationCalls++ },
		ObserveRequest: func(endpoint string, headers http.Header, body json.RawMessage) {
			parsed, _ := url.Parse(endpoint)
			observation.Requests = append(observation.Requests, fetchObservation{parsed.RequestURI(), "POST", headerPairs(headers), string(body)})
		},
	}, Emit: func(value Yield) (requestretry.NoticeAction, error) {
		if value.Notice != nil {
			return 0, errors.New("aborted response emitted retry notice")
		}
		update := value.Update
		var raw []byte
		var err error
		if update.Envelope != nil {
			raw, err = update.Envelope.MarshalJSON()
			live = append(live, update.Envelope.MarshalJSON)
		} else {
			raw, err = update.EventJSON()
			captured := append([]byte{}, raw...)
			live = append(live, func() ([]byte, error) { return captured, nil })
		}
		if err != nil {
			return 0, err
		}
		observation.Yielded = append(observation.Yielded, string(raw))
		if update.Envelope == nil {
			var part struct {
				Type  string `json:"type"`
				Index int    `json:"index"`
			}
			if err := json.Unmarshal(update.Event, &part); err != nil {
				return 0, err
			}
			if part.Type == input.AbortType && part.Index == input.AbortIndex {
				cancel()
			}
		}
		return requestretry.Resume, nil
	}})
	if err != nil || result.Reason != "caller_aborted" || result.Terminal {
		t.Fatalf("original cancelled model: %+v %v", result, err)
	}
	observation.Returned = true
	observation.Aborted = ctx.Err() != nil
	for _, snapshot := range live {
		raw, err := snapshot()
		if err != nil {
			t.Fatal(err)
		}
		observation.YieldedAfter = append(observation.YieldedAfter, string(raw))
	}
	for _, message := range original.Context.History {
		raw, err := message.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		observation.HistoryAfter = append(observation.HistoryAfter, string(protocol.StringifyValidatedJavaScriptJSON(raw)))
	}
	raw, err := json.Marshal(map[string]any{"observation": observation})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("STREAM_ABORT_RESULT:" + string(raw))
}
