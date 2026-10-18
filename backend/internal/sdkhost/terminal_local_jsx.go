package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func terminalLocalJSXName(name string) bool {
	return name == "mobile" || name == "plan" || name == "buddy" || name == "terminal-setup" || name == "branch" || name == "resume" || name == "diff" || name == "help"
}

// BuildTerminalLocalJSXOnOwner is the existing processSlashCommand onDone
// projection. Only the concrete migrated terminal handlers can call it; no
// externally supplied history or arbitrary command names are admitted.
func (h *Host) BuildTerminalLocalJSXOnOwner(scope *cooperative.Scope, name, args string, output *string, display string, shouldQuery bool) ([]protocol.JSONObject, error) {
	var value json.RawMessage
	if output != nil {
		value = quote(*output)
	}
	return h.BuildTerminalLocalJSXJSONOnOwner(scope, name, args, value, display, shouldQuery)
}

func (h *Host) BuildTerminalLocalJSXJSONOnOwner(scope *cooperative.Scope, name, args string, output json.RawMessage, display string, shouldQuery bool) ([]protocol.JSONObject, error) {
	if h == nil || scope == nil || !scope.BelongsTo(h.options.Owner) || h.options.Print == nil || !h.options.Print.NativeProcess || !h.options.Print.NativeInteractive || h.askRunning || h.closing || !terminalLocalJSXName(name) {
		return nil, cooperative.ErrRestricted
	}
	if display != "" && display != "user" && display != "system" && display != "skip" {
		return nil, cooperative.ErrRestricted
	}
	if display == "skip" {
		return nil, nil
	}
	tagged := "<command-name>/" + name + "</command-name>\n            <command-message>" + name + "</command-message>\n            <command-args>" + args + "</command-args>"
	text := quote("(no content)")
	if len(output) > 0 {
		units, err := protocol.DecodeJavaScriptJSONString(output)
		if err != nil {
			return nil, err
		}
		if len(units) > 0 {
			text = output
		}
	}
	var records []protocol.JSONObject
	if display == "system" {
		text = quote("undefined")
		if len(output) > 0 {
			text = output
		}
		stdout, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("<local-command-stdout>"), text, quote("</local-command-stdout>")}, "")
		if err != nil {
			return nil, err
		}
		printed, err := h.options.Factory.CreateLocalCommandInputJSON(stdout)
		if err != nil {
			return nil, err
		}
		records = []protocol.JSONObject{h.options.Factory.CreateLocalCommandInput(tagged), printed}
	} else {
		stdout, err := protocol.JoinJavaScriptValuesJSON([]json.RawMessage{quote("<local-command-stdout>"), text, quote("</local-command-stdout>")}, "")
		if err != nil {
			return nil, err
		}
		records = []protocol.JSONObject{messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": quote(tagged)})), messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": stdout}))}
		if !shouldQuery {
			if err := scope.AwaitFulfilled(); err != nil {
				return nil, err
			}
			records = append([]protocol.JSONObject{h.options.Factory.CreateLocalCaveat()}, records...)
		}
	}
	return records, nil
}

func (h *Host) CompleteTerminalLocalJSXJSONOnOwner(scope *cooperative.Scope, name, args string, output json.RawMessage, display string, shouldQuery bool) error {
	_, err := h.CompleteTerminalLocalJSXRecordsJSONOnOwner(scope, name, args, output, display, shouldQuery)
	return err
}

func (h *Host) CompleteTerminalLocalJSXRecordsJSONOnOwner(scope *cooperative.Scope, name, args string, output json.RawMessage, display string, shouldQuery bool) ([]protocol.JSONObject, error) {
	records, err := h.BuildTerminalLocalJSXJSONOnOwner(scope, name, args, output, display, shouldQuery)
	if err != nil || len(records) == 0 {
		return nil, err
	}
	if err = h.serial.AppendTerminalLocalJSX(scope, name, records); err != nil {
		return nil, err
	}
	result := make([]protocol.JSONObject, len(records))
	for i := range records {
		result[i] = records[i].Clone()
	}
	return result, nil
}

func (h *Host) CompleteTerminalLocalJSXOnOwner(scope *cooperative.Scope, name, args string, output *string, display string, shouldQuery bool) error {
	records, err := h.BuildTerminalLocalJSXOnOwner(scope, name, args, output, display, shouldQuery)
	if err != nil || len(records) == 0 {
		return err
	}
	return h.serial.AppendTerminalLocalJSX(scope, name, records)
}
