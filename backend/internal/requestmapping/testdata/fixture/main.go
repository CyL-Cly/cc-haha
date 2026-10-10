package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestmapping"
)

type mutation struct {
	Target    string            `json:"target"`
	Path      []json.RawMessage `json:"path"`
	Operation string            `json:"operation"`
	Key       json.RawMessage   `json:"key"`
	Value     json.RawMessage   `json:"value"`
}
type step struct {
	Operation      string          `json:"operation"`
	Message        json.RawMessage `json:"message"`
	MessageSource  *string         `json:"messageSource"`
	Messages       json.RawMessage `json:"messages"`
	AddCache       bool            `json:"addCache"`
	Enabled        bool            `json:"enabled"`
	SkipCacheWrite bool            `json:"skipCacheWrite"`
	QuerySource    *string         `json:"querySource"`
	CacheControl   json.RawMessage `json:"cacheControl"`
	Mutations      []mutation      `json:"mutations"`
	Repeat         bool            `json:"repeat"`
}

func snapshot(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	serialized, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		panic(err)
	}
	return serialized
}

func references(original, mapped *requestmapping.Value) any {
	if length, array := original.ArrayLength(); array {
		if _, mappedArray := mapped.ArrayLength(); mappedArray {
			facts := make([]any, 0, length)
			for index := 0; index < length; index++ {
				facts = append(facts, references(original.Element(index), mapped.Element(index)))
			}
			return facts
		}
	}
	before, after := original.Property("message").Property("content"), mapped.Property("content")
	var blockSame, nestedSame any
	if length, array := before.ArrayLength(); array {
		if _, mappedArray := after.ArrayLength(); mappedArray {
			blocks, nested := []bool{}, []bool{}
			for index := 0; index < length; index++ {
				blocks = append(blocks, before.Element(index) == after.Element(index))
				nested = append(nested, before.Element(index).Property("nested") == after.Element(index).Property("nested"))
			}
			blockSame, nestedSame = blocks, nested
		}
	}
	return map[string]any{"contentSame": before == after, "blockSame": blockSame, "nestedSame": nestedSame}
}

func selectChild(value *requestmapping.Value, key json.RawMessage) *requestmapping.Value {
	if len(key) > 0 && key[0] == '"' {
		var name string
		_ = json.Unmarshal(key, &name)
		return value.Property(name)
	}
	index, _ := strconv.Atoi(string(key))
	return value.Element(index)
}

func mutate(roots map[string]*requestmapping.Value, action mutation) error {
	target := roots[action.Target]
	for _, key := range action.Path {
		target = selectChild(target, key)
	}
	value, err := requestmapping.ParseValue(action.Value)
	if err != nil {
		return err
	}
	var changed bool
	if action.Operation == "append" {
		changed = target.Append(value)
	} else if len(action.Key) > 0 && action.Key[0] == '"' {
		var key string
		_ = json.Unmarshal(action.Key, &key)
		changed = target.SetProperty(key, value)
	} else {
		index, _ := strconv.Atoi(string(action.Key))
		changed = target.SetElement(index, value)
	}
	if !changed {
		return errors.New("unsupported request mapping fixture mutation")
	}
	return nil
}

func run() error {
	var input struct {
		HomeDir string `json:"homeDir"`
		Steps   []step `json:"steps"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.HomeDir == "" || input.HomeDir != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.HomeDir, ".claude") {
		return errors.New("request mapping fixture requires explicit disposable HOME/config")
	}
	results := []any{}
	for _, step := range input.Steps {
		raw := step.Message
		if step.MessageSource != nil {
			raw = json.RawMessage(*step.MessageSource)
		}
		if step.Operation == "breakpoints" {
			raw = step.Messages
		}
		var original *requestmapping.Value
		if len(raw) > 0 {
			var err error
			original, err = requestmapping.ParseValue(raw)
			if err != nil {
				return err
			}
		}
		events := []any{}
		control := step.CacheControl
		if len(control) == 0 {
			control = json.RawMessage(`{"type":"ephemeral"}`)
		}
		options := requestmapping.Options{QuerySource: step.QuerySource, SkipCacheWrite: step.SkipCacheWrite,
			CacheControl: func(source *string) (json.RawMessage, error) {
				events = append(events, map[string]any{"operation": "cache-control"})
				return control, nil
			},
			OnEvent: func(event requestmapping.BreakpointEvent) {
				events = append(events, map[string]any{"operation": "event", "name": "tengu_api_cache_breakpoints", "metadata": map[string]any{"totalMessageCount": event.TotalMessageCount, "cachingEnabled": event.CachingEnabled, "skipCacheWrite": event.SkipCacheWrite}})
			},
		}
		call := func() (*requestmapping.Value, error) {
			if step.Operation == "breakpoints" {
				length, _ := original.ArrayLength()
				messages := make([]*requestmapping.Value, 0, length)
				for index := 0; index < length; index++ {
					messages = append(messages, original.Element(index))
				}
				mapped, err := requestmapping.AddCacheBreakpoints(messages, step.Enabled, options)
				if err != nil {
					return nil, err
				}
				values := make([]*requestmapping.Value, 0, len(mapped))
				for _, message := range mapped {
					values = append(values, message.Value())
				}
				array, _ := requestmapping.ParseValue(json.RawMessage(`[]`))
				for _, value := range values {
					array.Append(value)
				}
				return array, nil
			}
			var mapped *requestmapping.MessageParam
			var err error
			switch step.Operation {
			case "user":
				mapped, err = requestmapping.MapUserMessage(original, step.AddCache, step.Enabled, options)
			case "assistant":
				mapped, err = requestmapping.MapAssistantMessage(original, step.AddCache, step.Enabled, options)
			default:
				return nil, fmt.Errorf("unsupported request mapping operation: %s", step.Operation)
			}
			if err != nil {
				return nil, err
			}
			return mapped.Value(), nil
		}
		output, err := call()
		if err != nil {
			var mappingError *requestmapping.MappingError
			if !errors.As(err, &mappingError) {
				return err
			}
			results = append(results, map[string]any{"error": map[string]any{"name": mappingError.Name, "message": mappingError.MessageJSON()}, "inputAfter": snapshot(original), "events": events})
			continue
		}
		initial, facts := snapshot(output), references(original, output)
		wire, err := output.MarshalJSON()
		if err != nil {
			return err
		}
		var repeat *requestmapping.Value
		var repeatFacts any
		if step.Repeat {
			repeat, err = call()
			if err != nil {
				return err
			}
			repeatFacts = references(original, repeat)
		}
		for _, action := range step.Mutations {
			if err := mutate(map[string]*requestmapping.Value{"input": original, "output": output, "repeat": repeat}, action); err != nil {
				return err
			}
		}
		result := map[string]any{"initial": initial, "initialWire": string(wire), "references": facts, "repeatReferences": repeatFacts, "outputAfter": snapshot(output), "inputAfter": snapshot(original), "events": events}
		if step.Repeat {
			result["repeatAfter"] = snapshot(repeat)
		}
		results = append(results, result)
	}
	_, err := fmt.Fprint(os.Stdout, string(snapshot(map[string]any{"results": results})))
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
