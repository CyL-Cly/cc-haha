package sdkhost

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func (h *Host) processNativePromptInput(scope *cooperative.Scope, name, args string, uuid json.RawMessage) (*sessioncontroller.ProcessedInput, error) {
	if process := h.options.Print.ProcessSkillPrompt; process != nil {
		history := make([]queryloop.Message, 0, len(h.options.Controller.History()))
		for _, record := range h.options.Controller.History() {
			history = append(history, queryloop.Message{Record: record})
		}
		return process(context.Background(), scope, name, args, history, uuid)
	}
	command, exists := h.options.Print.Registry.Prompt(name)
	if !exists {
		return nil, cap("unknown actual prompt command " + name)
	}
	metadata := command.Metadata()
	if metadata.String("context") == "fork" {
		return nil, cap("prompt command fork execution")
	}
	if metadata.Get("hooks") != nil {
		return nil, cap("prompt command hook registration")
	}
	var allowed []string
	_ = json.Unmarshal(metadata.Get("allowedTools"), &allowed)
	if len(allowed) != 0 {
		return nil, cap("prompt command permission grants")
	}
	invocation, err := h.options.Print.SkillInvocation(scope)
	if err != nil {
		return nil, err
	}
	text, err := command.Prompt(context.Background(), &args, invocation)
	if err != nil {
		return nil, err
	}
	if strings.Contains(text, "@") {
		return nil, cap("prompt command attachment discovery")
	}
	if string(metadata.Get("userInvocable")) == "false" {
		return nil, cap("model-only skill loading metadata")
	}
	skillPath := command.Name()
	if source := metadata.String("source"); source != "" {
		skillPath = source + ":" + skillPath
	}
	if err = h.options.Controller.InvokeSkill(scope, command.Name(), skillPath, text, h.agentID()); err != nil {
		return nil, err
	}
	loading := "<command-message>" + command.Name() + "</command-message>\n<command-name>/" + command.Name() + "</command-name>"
	if args != "" {
		loading += "\n<command-args>" + args + "</command-args>"
	}
	user := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": quote(loading), "uuid": uuid}))
	blocks := raw([]protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("text")}, protocol.JSONField{Name: "text", Value: quote(text)})})
	body := messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": blocks, "isMeta": raw(true)}))
	attachment := h.options.Factory.CreateAttachment(raw(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("command_permissions")}, protocol.JSONField{Name: "allowedTools", Value: raw([]string{})}, protocol.JSONField{Name: "model", Value: metadata.Get("model")})))
	return &sessioncontroller.ProcessedInput{Messages: []protocol.JSONObject{user, body, attachment}, Model: metadata.String("model"), Effort: metadata.Get("effort")}, nil
}
