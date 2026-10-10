package streamhistory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type fixtureInput struct {
	HomeDir              string   `json:"homeDir"`
	Wire                 string   `json:"wire"`
	HistoryPrefixSources []string `json:"historyPrefixSources"`
	Model                *string  `json:"model"`
	NonInteractive       *bool    `json:"nonInteractive"`
}
type assistantObservation struct {
	UUID              string `json:"uuid"`
	EnvelopeAtYield   string `json:"envelopeBytesAtYield"`
	MessageAtYield    string `json:"messageBytesAtYield"`
	EnvelopeAfter     string `json:"envelopeBytesAfterStream"`
	MessageAfter      string `json:"messageBytesAfterStream"`
	UsageStartAtYield bool   `json:"usageSharesStartAtYield"`
	UsageStartAfter   bool   `json:"usageSharesStartAfterStream"`
	UsageAliasGroup   int    `json:"usageAliasGroup"`
	APIError          bool   `json:"isApiError"`
}
type eventObservation struct {
	Envelope string `json:"envelopeBytes"`
	Event    string `json:"eventBytes"`
}
type contentAlias struct {
	AssistantIndex int `json:"assistantIndex"`
	BlockIndex     int `json:"blockIndex"`
}
type normalizedObservation struct {
	Envelope       string          `json:"envelopeBytes"`
	Message        string          `json:"messageBytes"`
	SourceIndex    *int            `json:"sourceAssistantIndex"`
	UsageAliases   []int           `json:"usageAliasesAssistantIndexes"`
	ContentAliases []*contentAlias `json:"contentAliases"`
}
type observedError struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}
type fixtureObservation struct {
	Profile            map[string]any          `json:"profile"`
	Wire               string                  `json:"wire"`
	HistoryPrefix      []string                `json:"historyPrefixBytes"`
	StartMessage       *string                 `json:"startMessageBytes"`
	StartUsage         *string                 `json:"startUsageBytes"`
	Events             []eventObservation      `json:"events"`
	Assistants         []assistantObservation  `json:"assistants"`
	Timeline           []map[string]any        `json:"timeline"`
	UUIDCalls          int                     `json:"uuidCalls"`
	TimeCalls          int                     `json:"timeCalls"`
	Normalized         []normalizedObservation `json:"normalized"`
	HistoryAfter       []string                `json:"historyBytesAfterNormalization"`
	Error              *observedError          `json:"error"`
	NormalizationError *observedError          `json:"normalizationError"`
	FetchCount         int                     `json:"fetchCount"`
}

func fixtureString(raw []byte) string {
	normalized, _ := protocol.ParseAndStringifyJavaScriptJSON(raw)
	return string(normalized)
}

// External producer failures must stay outside Controller EOF/feedback policy.
// The HTTP fixture uses this marker without rewriting the original cause.
type fixtureProducerFailure struct{ cause error }

func (failure *fixtureProducerFailure) Error() string { return failure.cause.Error() }
func (failure *fixtureProducerFailure) Unwrap() error { return failure.cause }

// This test-only projection observes production references and exact bytes.
// It does not implement the commit or normalization decisions being compared.
func observeFixture(input fixtureInput) (fixtureObservation, error) {
	requestID := "fixed-request"
	return observeFixtureFromParts(input, &requestID, func(feed func(json.RawMessage) error) error {
		decoder := protocol.NewSSEDecoder(strings.NewReader(input.Wire))
		for {
			event, err := decoder.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			// This handed-response fixture covers the existing six event kinds;
			// it is not the production SDK dispatch adapter or an HTTP proof.
			switch event.Event {
			case "message_start", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop":
			default:
				continue
			}
			if err := feed(json.RawMessage(event.Data)); err != nil {
				return err
			}
		}
	})
}

// The observation projection is shared by handed-response and real HTTP
// fixtures. The supplied producer owns transport/SDK dispatch; this helper
// calls the real Controller and normalization without reimplementing them.
func observeFixtureFromParts(input fixtureInput, requestID *string, consume func(func(json.RawMessage) error) error) (fixtureObservation, error) {
	model, nonInteractive := "fixture-model", true
	if input.Model != nil {
		model = *input.Model
	}
	if input.NonInteractive != nil {
		nonInteractive = *input.NonInteractive
	}
	result := fixtureObservation{Profile: map[string]any{"tools": []any{}, "thinking": "disabled", "querySource": "sdk", "nonStreamingFallback": "disabled", "nodeEnv": "production", "maxOutputTokens": 32000, "model": model, "nonInteractive": nonInteractive}, Wire: input.Wire, HistoryPrefix: []string{}, Events: []eventObservation{}, Assistants: []assistantObservation{}, Timeline: []map[string]any{}, Normalized: []normalizedObservation{}, HistoryAfter: []string{}, FetchCount: 1}
	phase := "query"
	controller, err := New(Options{UUID: func() string {
		result.UUIDCalls++
		value := fmt.Sprintf("fixture-id-%d", result.UUIDCalls)
		result.Timeline = append(result.Timeline, map[string]any{"kind": "uuid", "phase": phase, "value": value})
		return value
	}, Now: func() time.Time {
		result.TimeCalls++
		result.Timeline = append(result.Timeline, map[string]any{"kind": "time", "phase": phase, "value": "2023-11-14T22:13:20.000Z"})
		return time.UnixMilli(1700000000000)
	}, RequestID: requestID, RefusalProfile: &RefusalProfile{Model: model, NonInteractive: nonInteractive}})
	if err != nil {
		return result, err
	}
	userSource := `{"type":"user","message":{"role":"user","content":"Stream history fixture"},"uuid":"fixture-user","timestamp":"2023-11-14T22:13:20.000Z"}`
	if len(input.HistoryPrefixSources) > 0 {
		if len(input.HistoryPrefixSources) != 1 {
			return result, ErrUnsupported
		}
		userSource = input.HistoryPrefixSources[0]
	}
	var user protocol.JSONObject
	if err := json.Unmarshal([]byte(userSource), &user); err != nil {
		return result, err
	}
	result.HistoryPrefix = append(result.HistoryPrefix, fixtureString([]byte(userSource)))
	committed := []*Envelope{}
	emit := func(update Update) error {
		if update.Envelope != nil {
			envelope := update.Envelope
			raw, err := envelope.MarshalJSON()
			if err != nil {
				return err
			}
			message, err := envelope.MessageJSON()
			if err != nil {
				return err
			}
			committed = append(committed, envelope)
			result.Assistants = append(result.Assistants, assistantObservation{UUID: envelope.UUID, EnvelopeAtYield: string(raw), MessageAtYield: string(message), UsageStartAtYield: envelope.Usage == controller.StartUsage(), UsageAliasGroup: -1, APIError: envelope.APIError})
			result.Timeline = append(result.Timeline, map[string]any{"kind": "assistant", "phase": phase, "assistantIndex": len(committed) - 1, "uuid": envelope.UUID})
			return nil
		}
		var event protocol.JSONObject
		if err := json.Unmarshal(update.Event, &event); err != nil {
			return err
		}
		if event.String("type") == "message_start" {
			message := fixtureString(controller.StartJSON())
			result.StartMessage = &message
			if usage := controller.StartUsage(); usage != nil {
				raw := fixtureString(usage.Raw)
				result.StartUsage = &raw
			}
		}
		wrapper, err := update.EventJSON()
		if err != nil {
			return err
		}
		row := map[string]any{"kind": "stream_event", "phase": phase, "eventIndex": len(result.Events), "eventType": event.String("type")}
		if index := event.Get("index"); index != nil {
			row["blockIndex"] = index
		}
		result.Timeline = append(result.Timeline, row)
		result.Events = append(result.Events, eventObservation{Envelope: string(wrapper), Event: string(update.Event)})
		return nil
	}
	cause := consume(func(raw json.RawMessage) error { return controller.Feed(raw, Timing{}, emit) })
	var producerFailure *fixtureProducerFailure
	if errors.As(cause, &producerFailure) {
		return result, cause // Do not invoke a counterfeit Finish policy.
	}
	if err := controller.Finish(cause, emit); err != nil {
		result.Error = &observedError{Name: "Error", Message: err.Error()}
	}
	usageGroups := map[*Usage]int{controller.StartUsage(): 0}
	for index, envelope := range committed {
		raw, err := envelope.MarshalJSON()
		if err != nil {
			return result, err
		}
		message, err := envelope.MessageJSON()
		if err != nil {
			return result, err
		}
		row := &result.Assistants[index]
		row.EnvelopeAfter = string(raw)
		row.MessageAfter = string(message)
		row.UsageStartAfter = envelope.Usage == controller.StartUsage()
		if _, exists := usageGroups[envelope.Usage]; !exists {
			usageGroups[envelope.Usage] = len(usageGroups)
		}
		row.UsageAliasGroup = usageGroups[envelope.Usage]
	}
	phase = "normalize"
	normalized, err := NormalizeTurn(user, committed)
	if err != nil {
		result.NormalizationError = &observedError{Name: "Error", Message: err.Error()}
	} else {
		for _, message := range normalized {
			raw, err := message.MarshalJSON()
			if err != nil {
				return result, err
			}
			api, err := message.MessageJSON()
			if err != nil {
				return result, err
			}
			row := normalizedObservation{Envelope: string(raw), Message: string(api), UsageAliases: []int{}, ContentAliases: []*contentAlias{}}
			if envelope := message.Assistant; envelope != nil {
				for index, original := range committed {
					if original.UUID == envelope.UUID {
						value := index
						row.SourceIndex = &value
					}
					if original.Usage == envelope.Usage {
						row.UsageAliases = append(row.UsageAliases, index)
					}
				}
				for _, block := range envelope.Content {
					var alias *contentAlias
					for assistantIndex, original := range committed {
						for blockIndex, originalBlock := range original.Content {
							if originalBlock == block {
								alias = &contentAlias{AssistantIndex: assistantIndex, BlockIndex: blockIndex}
								break
							}
						}
						if alias != nil {
							break
						}
					}
					row.ContentAliases = append(row.ContentAliases, alias)
				}
			}
			result.Normalized = append(result.Normalized, row)
		}
	}
	result.HistoryAfter = append(result.HistoryAfter, result.HistoryPrefix...)
	for _, envelope := range committed {
		raw, err := envelope.MarshalJSON()
		if err != nil {
			return result, err
		}
		result.HistoryAfter = append(result.HistoryAfter, string(raw))
	}
	return result, nil
}

func TestStreamHistoryFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_STREAM_HISTORY_FIXTURE") != "1" {
		t.Skip("used only by the hermetic differential lane")
	}
	var input fixtureInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		t.Fatal(err)
	}
	if input.HomeDir == "" || input.HomeDir != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.HomeDir, ".claude") {
		t.Fatal("explicit disposable HOME/config required")
	}
	result, err := observeFixture(input)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("STREAM_HISTORY_RESULT:%s\n", raw)
}
