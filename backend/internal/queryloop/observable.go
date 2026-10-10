package queryloop

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// Plain JSON objects inherit these names from Object.prototype. Source tests
// added fields with `k in originalInput`, not hasOwnProperty. Object keys use
// storage's UTF-16 key identity so distinct lone surrogate keys do not merge.
var objectPrototypeNames = map[string]bool{"constructor": true, "__defineGetter__": true, "__defineSetter__": true, "hasOwnProperty": true, "__lookupGetter__": true, "__lookupSetter__": true, "isPrototypeOf": true, "propertyIsEnumerable": true, "toString": true, "valueOf": true, "__proto__": true, "toLocaleString": true}

func addedOwnField(original, updated protocol.JSONObject) (bool, error) {
	beforeRaw, err := original.MarshalJSON()
	if err != nil {
		return false, err
	}
	afterRaw, err := updated.MarshalJSON()
	if err != nil {
		return false, err
	}
	var before, after storage.Object
	if err = json.Unmarshal(beforeRaw, &before); err != nil {
		return false, err
	}
	if err = json.Unmarshal(afterRaw, &after); err != nil {
		return false, err
	}
	for key := range after {
		if _, exists := before[key]; !exists && !objectPrototypeNames[key] {
			return true, nil
		}
	}
	return false, nil
}
func observableEnvelopeJSON(original *streamhistory.Envelope, lookup func(json.RawMessage) (Backfill, error)) (*streamhistory.Envelope, error) {
	var copy *streamhistory.Envelope
	for index, block := range original.Content {
		if block.Object.String("type") != "tool_use" {
			continue
		}
		raw := bytes.TrimSpace(block.Object.Get("input"))
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || raw[0] != '{' && raw[0] != '[' {
			continue
		}
		name, err := modelJSONString(block.Object.Get("name"), "name")
		if err != nil {
			return nil, err
		}
		backfill, err := lookup(name)
		if err != nil {
			return nil, err
		}
		if backfill == nil {
			continue
		}
		if raw[0] == '[' {
			return nil, unsupported("array input observable spread", ErrUnsupported)
		}
		var input protocol.JSONObject
		if err = json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		updated := input.Clone()
		if err = backfill(&updated); err != nil {
			return nil, err
		}
		if !updated.IsObject() {
			return nil, errors.New("observable backfill must preserve the plain input object")
		}
		added, err := addedOwnField(input, updated)
		if err != nil {
			return nil, err
		}
		if !added {
			continue
		}
		if copy == nil {
			value := *original
			value.Record = original.Record.Clone()
			value.Message = original.Message.Clone()
			value.Content = append([]*streamhistory.Block(nil), original.Content...)
			copy = &value
		}
		changed := block.Object.Clone()
		serialized, err := updated.MarshalJSON()
		if err != nil {
			return nil, err
		}
		changed.Set("input", serialized)
		copy.Content[index] = &streamhistory.Block{Object: changed}
	}
	if copy != nil {
		return copy, nil
	}
	return original, nil
}

// A scalar registry cannot contain an unpaired surrogate, so that name has no
// backfill. The legacy callback is never given the replacement character.
func observableEnvelope(original *streamhistory.Envelope, lookup func(string) (Backfill, error)) (*streamhistory.Envelope, error) {
	return observableEnvelopeJSON(original, func(raw json.RawMessage) (Backfill, error) {
		name, err := modelExecutionString(raw, "name")
		if err != nil {
			return nil, nil
		}
		return lookup(name)
	})
}
