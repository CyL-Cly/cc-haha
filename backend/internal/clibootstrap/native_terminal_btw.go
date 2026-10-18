package clibootstrap

import (
	"context"
	"encoding/json"
	"math"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This is the mounted command's real GlobalConfig update, independent of the
// parent's running ASK. The side query uses the existing sealed Host producer.
func (m *MainPrint) PrepareTerminalBtw(ctx context.Context, args string) (string, error) {
	question, _, err := m.PrepareTerminalBtwState(ctx, args)
	return question, err
}

func (m *MainPrint) PrepareTerminalBtwState(ctx context.Context, args string) (string, bool, error) {
	question := protocol.TrimJavaScriptString(args)
	var immediate bool
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.Host == nil || m.closed || m.options.Native == nil || !m.options.Interactive || m.agentChild || m.teamWorker != nil {
			return cooperative.ErrRestricted
		}
		var stateErr error
		immediate, stateErr = m.Host.TerminalBtwImmediateOnOwner(scope)
		if stateErr != nil {
			return stateErr
		}
		if question == "" {
			return nil
		}
		return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			next := current.Clone()
			value, err := terminalBtwCountIncrement(current.Get("btwUseCount"))
			if err != nil {
				return nil, err
			}
			next.Set("btwUseCount", value)
			return &next, nil
		})
	})
	if err == nil {
		err = receipt.Wait()
	}
	return question, immediate, err
}

func terminalBtwCountIncrement(value json.RawMessage) (json.RawMessage, error) {
	if len(value) == 0 {
		return json.RawMessage("null"), nil
	}
	if value[0] == '"' || value[0] == '[' || value[0] == '{' {
		text, err := protocol.JavaScriptStringJSON(value)
		if err != nil {
			return nil, err
		}
		return protocol.JoinJavaScriptValuesJSON([]json.RawMessage{text, json.RawMessage(`"1"`)}, "")
	}
	number := float64(0)
	switch string(value) {
	case "null", "false":
	case "true":
		number = 1
	default:
		var err error
		number, err = strconv.ParseFloat(string(value), 64)
		if err != nil && !math.IsInf(number, 0) {
			return nil, err
		}
	}
	number++
	if math.IsInf(number, 0) || math.IsNaN(number) {
		return json.RawMessage("null"), nil
	}
	return json.Marshal(number)
}
