package clibootstrap

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Keep the debounce in the process-shared skill state, including forked
// agents. Source stores it before attempting the synchronous config write.
func (m *MainPrint) recordNativeSkillUsage(scope *cooperative.Scope, name string) error {
	return m.recordNativeSkillUsageJSON(scope, quoteMain(name))
}

func (m *MainPrint) recordNativeSkillUsageJSON(scope *cooperative.Scope, name json.RawMessage) error {
	units, err := protocol.DecodeJavaScriptJSONString(name)
	if err != nil {
		return err
	}
	name = protocol.QuoteJavaScriptUTF16(units)
	identity := string(name)
	if !scope.BelongsTo(m.Owner) || m.Global == nil || m.options.Native == nil {
		return cooperative.ErrRestricted
	}
	m.ensureNativeSkillState()
	now := m.options.Native.NowMillis()
	if previous, present := m.skillListing.usageWrites[identity]; present && now-previous < 60_000 {
		return nil
	}
	m.skillListing.usageWrites[identity] = now
	return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		usage := protocol.NewJSONObject()
		if raw := current.Get("skillUsage"); len(raw) > 0 && string(raw) != "null" {
			if parsed, e := nativeSkillUsageSpread(raw); e == nil {
				usage = parsed
			} else {
				return nil, e
			}
		}
		existing, _, err := usage.ObjectPropertyJSON(name)
		if err != nil {
			return nil, err
		}
		count, e := nativeSkillIncrement(existing.Get("usageCount"))
		if e != nil {
			return nil, e
		}
		if err := usage.SetPropertyJSON(name, rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "usageCount", Value: count}, protocol.JSONField{Name: "lastUsedAt", Value: rawMain(now)}))); err != nil {
			return nil, err
		}
		next.Set("skillUsage", rawMain(usage))
		return &next, nil
	})
}

func nativeSkillUsageSpread(value json.RawMessage) (protocol.JSONObject, error) {
	value = bytes.TrimSpace(value)
	out := protocol.NewJSONObject()
	if len(value) == 0 || value[0] != '{' && value[0] != '[' && value[0] != '"' {
		return out, nil
	}
	if value[0] == '{' {
		e := out.UnmarshalJSON(value)
		return out, e
	}
	if value[0] == '[' {
		var rows []json.RawMessage
		if e := json.Unmarshal(value, &rows); e != nil {
			return out, e
		}
		for i, row := range rows {
			out.Set(strconv.Itoa(i), row)
		}
		return out, nil
	}
	units, e := protocol.DecodeJavaScriptJSONString(value)
	if e != nil {
		return out, e
	}
	for i, unit := range units {
		out.Set(strconv.Itoa(i), protocol.QuoteJavaScriptUTF16([]uint16{unit}))
	}
	return out, nil
}

// (existing?.usageCount ?? 0) + 1 uses JS addition, including old user-owned
// malformed scalar values. Preserve string/array/object coercion rather than
// replacing those values with a different numeric count.
func nativeSkillIncrement(value json.RawMessage) (json.RawMessage, error) {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return json.RawMessage("1"), nil
	}
	if value[0] == '"' || value[0] == '[' || value[0] == '{' {
		converted, e := protocol.JavaScriptStringJSON(value)
		if e != nil {
			return nil, e
		}
		return protocol.JoinJavaScriptValuesJSON([]json.RawMessage{converted, json.RawMessage("1")}, "")
	}
	var number float64
	if bytes.Equal(value, []byte("true")) {
		number = 1
	} else if !bytes.Equal(value, []byte("false")) {
		var e error
		number, e = strconv.ParseFloat(string(value), 64)
		if e != nil && !math.IsInf(number, 0) {
			return nil, e
		}
	}
	number++
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return json.RawMessage("null"), nil
	}
	return rawMain(number), nil
}
