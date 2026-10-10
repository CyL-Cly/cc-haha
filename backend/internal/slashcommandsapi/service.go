// Package slashcommandsapi composes the original desktop slash menu from
// actual Skill metadata, the shared command loader and compiled declarations.
// It never opens a Session or invokes an Agent/model.
package slashcommandsapi

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillregistry"
)

type Command struct {
	Name         string          `json:"name"`
	Description  json.RawMessage `json:"description"`
	ArgumentHint json.RawMessage `json:"argumentHint,omitempty"`
	Kind         string          `json:"kind,omitempty"`
	Source       string          `json:"source,omitempty"`
	// Keep wire names separate from their Go display spelling: JavaScript Map
	// distinguishes lone UTF-16 surrogates from each other and from U+FFFD.
	nameJSON json.RawMessage
}

func (c Command) MarshalJSON() ([]byte, error) {
	name := c.nameJSON
	if len(name) == 0 {
		name, _ = json.Marshal(c.Name)
	}
	desc := c.Description
	if len(desc) == 0 {
		desc = json.RawMessage("null")
	}
	value := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: name}, protocol.JSONField{Name: "description", Value: desc})
	if len(c.ArgumentHint) > 0 {
		value.Set("argumentHint", c.ArgumentHint)
	}
	for _, field := range []struct{ key, text string }{{"kind", c.Kind}, {"source", c.Source}} {
		if field.text != "" {
			raw, _ := json.Marshal(field.text)
			value.Set(field.key, raw)
		}
	}
	return value.MarshalJSON()
}

func commandKey(raw json.RawMessage, name string) string {
	if len(raw) == 0 {
		raw, _ = json.Marshal(name)
	}
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil || len(units) == 0 {
		return ""
	}
	return string(protocol.QuoteJavaScriptUTF16(units))
}

type Options struct {
	Skills           *skillregistry.Service
	LocalCommands    func(context.Context, string) ([]protocol.JSONObject, error)
	CompiledCommands func(context.Context) ([]protocol.JSONObject, error)
	Compare          func(string, string) int
	Warn             func(error)
}
type Service struct{ options Options }

func New(o Options) (*Service, error) {
	if o.Skills == nil || o.LocalCommands == nil || o.CompiledCommands == nil || o.Compare == nil || o.Warn == nil {
		return nil, errors.New("slash commands require actual Skill/command/compiled/collation/error producers")
	}
	return &Service{options: o}, nil
}

func (s *Service) List(ctx context.Context, cwd string, includeCompiled bool) ([]Command, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	skills, e := s.options.Skills.List(ctx, cwd)
	if e != nil {
		return nil, e
	}
	local, e := s.options.LocalCommands(ctx, cwd)
	if e != nil {
		return nil, e
	}
	type claim struct {
		command Command
		flavor  string
	}
	claims := map[string]claim{}
	for _, skill := range skills {
		if !skill.UserInvocable {
			continue
		}
		previous, ok := claims[skill.Name]
		if ok && !skillregistry.Outranks(previous.flavor, skill.RootFlavor) {
			continue
		}
		claims[skill.Name] = claim{Command{Name: skill.Name, Description: description(skill.Description), Kind: "skill", Source: skill.Source}, skill.RootFlavor}
	}
	for _, value := range local {
		if value.String("type") != "prompt" || value.String("loadedFrom") != "commands_DEPRECATED" || string(value.Get("userInvocable")) == "false" || string(value.Get("isHidden")) == "true" {
			continue
		}
		name := value.String("name")
		if _, ok := claims[name]; ok {
			continue
		}
		claims[name] = claim{command: Command{Name: name, Description: description(value.Get("description")), ArgumentHint: truthyRaw(value.Get("argumentHint")), Kind: "command"}}
	}
	if includeCompiled {
		compiled, e := s.options.CompiledCommands(ctx)
		if e != nil {
			s.options.Warn(e)
		} else {
			for _, value := range compiled {
				name := value.String("name")
				if _, ok := claims[name]; ok {
					continue
				}
				claims[name] = claim{command: Command{Name: name, Description: description(value.Get("description")), ArgumentHint: truthyRaw(value.Get("argumentHint"))}}
			}
		}
	}
	result := make([]Command, 0, len(claims))
	for _, value := range claims {
		result = append(result, value.command)
	}
	sort.SliceStable(result, func(i, j int) bool { return s.options.Compare(result[i].Name, result[j].Name) < 0 })
	return result, ctx.Err()
}

// Merge keeps the real CLI list's order/metadata and enriches only its gaps.
// Map.set replacement retains the first insertion position, including duplicate
// preferred names. Compiled commands must already have been excluded here.
func Merge(preferred []protocol.JSONObject, fallback []Command) []Command {
	fallbackByName := map[string]Command{}
	for _, command := range fallback {
		if command.Name != "" {
			fallbackByName[commandKey(command.nameJSON, command.Name)] = command
		}
	}
	result := []Command{}
	positions := map[string]int{}
	for _, value := range preferred {
		name := value.String("name")
		key := commandKey(value.Get("name"), "")
		if key == "" {
			continue
		}
		base, exists := fallbackByName[key]
		command := Command{Name: name, nameJSON: append(json.RawMessage(nil), value.Get("name")...), Description: description(value.Get("description")), ArgumentHint: truthyRaw(value.Get("argumentHint")), Kind: "command"}
		if exists {
			if truthyRaw(command.Description) == nil {
				command.Description = base.Description
			}
			if truthyRaw(command.ArgumentHint) == nil {
				command.ArgumentHint = base.ArgumentHint
			}
			if base.Kind != "" {
				command.Kind = base.Kind
			}
			command.Source = base.Source
		}
		if index, ok := positions[key]; ok {
			result[index] = command
		} else {
			positions[key] = len(result)
			result = append(result, command)
		}
	}
	for _, command := range fallback {
		if command.Name == "" {
			continue
		}
		key := commandKey(command.nameJSON, command.Name)
		if _, ok := positions[key]; ok {
			continue
		}
		positions[key] = len(result)
		result = append(result, command)
	}
	return result
}

func truthyRaw(raw json.RawMessage) json.RawMessage {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	switch v := value.(type) {
	case nil:
		return nil
	case bool:
		if !v {
			return nil
		}
	case string:
		if v == "" {
			return nil
		}
	case float64:
		if v == 0 {
			return nil
		}
	}
	return append(json.RawMessage(nil), raw...)
}
func description(raw json.RawMessage) json.RawMessage {
	if value := truthyRaw(raw); value != nil {
		return value
	}
	return json.RawMessage(`""`)
}
