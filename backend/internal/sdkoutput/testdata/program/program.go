// Package program is an original-input fixture interpreter, never a production
// SDK host. Assertion files are not read by this interpreter.
package program

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkoutput"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"time"
)

type Operation struct {
	Op       string                 `json:"op"`
	Key      string                 `json:"key"`
	Out      string                 `json:"out"`
	Value    string                 `json:"value"`
	Keys     []string               `json:"keys"`
	Field    string                 `json:"field"`
	Index    int                    `json:"index"`
	Millis   int64                  `json:"millis"`
	After    map[string][]Operation `json:"after"`
	RawValue json.RawMessage        `json:"-"`
}

func (operation *Operation) UnmarshalJSON(raw []byte) error {
	type plain Operation
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*operation = Operation(value)
	operation.RawValue = append(json.RawMessage(nil), fields["value"]...)
	return nil
}

type Program struct {
	Name       string      `json:"name"`
	Operations []Operation `json:"operations"`
}
type Input struct {
	SID      json.RawMessage `json:"sid"`
	BaseNow  int64           `json:"baseNow"`
	Programs []Program       `json:"programs"`
	Home     string          `json:"home,omitempty"`
}
type Allocation struct {
	Kind  string `json:"kind"`
	Value any    `json:"value"`
}
type Observation struct {
	Op     string   `json:"op"`
	Out    string   `json:"out"`
	Values []string `json:"values"`
}
type Result struct {
	Name         string        `json:"name"`
	Observations []Observation `json:"observations"`
	Allocations  []Allocation  `json:"allocations"`
}

func Run(input Input) ([]Result, error) {
	counter := 0
	now := input.BaseNow
	sid := input.SID
	allocations := []Allocation{}
	env := map[string]string{}
	factory, _ := messages.NewFactory(func() string {
		counter++
		value := fmt.Sprintf("00000000-0000-4000-8000-%012d", counter)
		allocations = append(allocations, Allocation{"uuid", value})
		return value
	}, func() time.Time {
		value := time.UnixMilli(now).UTC()
		allocations = append(allocations, Allocation{"time", value.Format("2006-01-02T15:04:05.000Z")})
		return value
	})
	normalizer, _ := sdkoutput.New(sdkoutput.Dependencies{Factory: factory, SessionID: func() json.RawMessage { return append(json.RawMessage(nil), sid...) }, NowMillis: func() float64 { allocations = append(allocations, Allocation{"now", now}); return float64(now) }, OriginalEnv: func(key string) string { return env[key] }})
	results := []Result{}
	for _, program := range input.Programs {
		counter = 0
		now = input.BaseNow
		sid = input.SID
		allocations = []Allocation{}
		env = map[string]string{}
		objects := map[string]sdkoutput.Message{}
		outputs := map[string][]*sdkoutput.View{}
		observations := []Observation{}
		for _, op := range program.Operations {
			var err error
			switch op.Op {
			case "raw":
				objects[op.Key], err = sdkoutput.Decode([]byte(op.Value))
			case "factory-user", "factory-assistant", "factory-error":
				var options storage.Object
				if err = json.Unmarshal([]byte(op.Value), &options); err == nil {
					if op.Op == "factory-user" {
						objects[op.Key], err = sdkoutput.User(factory, options)
					} else {
						objects[op.Key], err = sdkoutput.Assistant(factory, options, op.Op == "factory-error")
					}
				}
			case "sdk", "many":
				values := []*sdkoutput.View{}
				if op.Op == "sdk" {
					err = normalizer.NormalizeMessage(objects[op.Key], func(view *sdkoutput.View) error { values = append(values, view); return nil })
				} else {
					original := []sdkoutput.Message{}
					for _, key := range op.Keys {
						original = append(original, objects[key])
					}
					values, err = normalizer.NormalizeMessages(original)
				}
				outputs[op.Out] = values
				if err == nil {
					encoded, e := observe(values)
					err = e
					observations = append(observations, Observation{op.Op, op.Out, encoded})
				}
			case "sdk-controlled":
				values := []*sdkoutput.View{}
				index := 0
				stop := errors.New("original consumer stop")
				err = normalizer.NormalizeMessage(objects[op.Key], func(view *sdkoutput.View) error {
					values = append(values, view)
					for _, action := range op.After[fmt.Sprint(index)] {
						switch action.Op {
						case "session":
							sid = action.RawValue
						case "api-set":
							original := objects[action.Key]
							original.API.Set(action.Field, json.RawMessage(action.Value))
						case "record-set":
							original := objects[action.Key]
							original.Record.Set(action.Field, json.RawMessage(action.Value))
						case "block-set":
							objects[action.Key].Content[action.Index].Object.Set(action.Field, json.RawMessage(action.Value))
						case "stop":
							return stop
						default:
							return fmt.Errorf("unsupported original yield action")
						}
					}
					index++
					return nil
				})
				if errors.Is(err, stop) {
					err = nil
				}
				outputs[op.Out] = values
				if err == nil {
					encoded, e := observe(values)
					err = e
					observations = append(observations, Observation{op.Op, op.Out, encoded})
				}
			case "observe":
				encoded, e := observe(outputs[op.Out])
				err = e
				observations = append(observations, Observation{op.Op, op.Out, encoded})
			case "api-set":
				message := objects[op.Key]
				message.API.Set(op.Field, json.RawMessage(op.Value))
			case "record-set":
				message := objects[op.Key]
				message.Record.Set(op.Field, json.RawMessage(op.Value))
			case "block-set":
				message := objects[op.Key]
				message.Content[op.Index].Object.Set(op.Field, json.RawMessage(op.Value))
			case "clock":
				now = op.Millis
			case "session":
				sid = op.RawValue
			case "env":
				if op.Field != "CLAUDE_CODE_REMOTE" && op.Field != "CLAUDE_CODE_CONTAINER_ID" {
					return nil, fmt.Errorf("original env field unsupported")
				}
				env[op.Field] = op.Value
			default:
				return nil, fmt.Errorf("original operation unsupported: %s", op.Op)
			}
			if err != nil {
				return nil, fmt.Errorf("%s/%s: %w", program.Name, op.Op, err)
			}
		}
		results = append(results, Result{program.Name, observations, allocations})
	}
	return results, nil
}
func observe(values []*sdkoutput.View) ([]string, error) {
	result := []string{}
	for _, value := range values {
		raw, err := value.MarshalJSON()
		if err != nil {
			return nil, err
		}
		result = append(result, string(raw))
	}
	return result, nil
}
