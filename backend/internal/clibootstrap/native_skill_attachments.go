package clibootstrap

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

type nativeSkillListingState struct {
	sent                 map[string]map[string]bool
	usageWrites          map[string]float64
	gitLoaded, gitExists bool
	consumeSuppression   func(*cooperative.Scope) (bool, error)
}

func (m *MainPrint) ensureNativeSkillState() {
	if m.skillListing == nil {
		m.skillListing = &nativeSkillListingState{sent: map[string]map[string]bool{}, usageWrites: map[string]float64{}, consumeSuppression: func(scope *cooperative.Scope) (bool, error) {
			if m.Controller == nil {
				return false, cooperative.ErrRestricted
			}
			return m.Controller.ConsumeSkillListingSuppression(scope)
		}}
	}
}
func (m *MainPrint) resetNativeSkillListing(scope *cooperative.Scope) error {
	if m.skillListing != nil {
		m.skillListing.sent = map[string]map[string]bool{}
		_, e := m.skillListing.consumeSuppression(scope)
		return e
	}
	return nil
}
func (m *MainPrint) nativeSkillAttachments(scope *cooperative.Scope, _ []queryloop.Message, toolContexts ...*builtinwrite.CallContext) ([]queryloop.Message, error) {
	if !scope.BelongsTo(m.Owner) || m.skillCommands == nil {
		return nil, cooperative.ErrRestricted
	}
	out := []queryloop.Message{}
	current, e := m.state.Context(scope)
	if e != nil {
		return nil, e
	}
	cwd, e := current.GetCWD(scope)
	if e != nil {
		return nil, e
	}
	directories := append([]string(nil), m.dynamicSkillDirTriggers...)
	pending := make([]*ownerasync.OwnerFuture[[]string], 0, len(directories))
	for _, dir := range directories {
		names, e := nativeSkillWork(m, scope, func() ([]string, error) {
			file, e := os.Open(dir)
			if e != nil {
				return []string{}, nil
			}
			defer file.Close()
			entries, e := file.ReadDir(-1)
			if e != nil {
				return []string{}, nil
			}
			candidates := []string{}
			for _, entry := range entries {
				if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
					continue
				}
				candidates = append(candidates, entry.Name())
			}
			checked := make([]bool, len(candidates))
			var wait sync.WaitGroup
			for i, name := range candidates {
				wait.Add(1)
				go func() {
					defer wait.Done()
					_, e := os.Stat(filepath.Join(dir, name, "SKILL.md"))
					checked[i] = e == nil
				}()
			}
			wait.Wait()
			names := []string{}
			for i, name := range candidates {
				if checked[i] {
					names = append(names, name)
				}
			}
			return names, nil
		})
		if e != nil {
			return nil, e
		}
		pending = append(pending, names)
	}
	// Promise.all starts all directory/stat primitives before joining them and
	// only creates attachment identities once every result is available.
	results := make([][]string, len(pending))
	for i, names := range pending {
		results[i], e = names.Await(scope)
		if e != nil {
			return nil, e
		}
	}
	for i, skillNames := range results {
		dir := directories[i]
		if len(skillNames) > 0 {
			display, e := filepath.Rel(cwd, dir)
			if e != nil {
				return nil, e
			}
			out = append(out, queryloop.Message{Record: m.factory.CreateAttachment(rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("dynamic_skill")}, protocol.JSONField{Name: "skillDir", Value: quoteMain(dir)}, protocol.JSONField{Name: "skillNames", Value: rawMain(skillNames)}, protocol.JSONField{Name: "displayPath", Value: quoteMain(display)})))})
		}
	}
	m.dynamicSkillDirTriggers = nil
	if m.options.Native.Environment["NODE_ENV"] == "test" {
		return out, nil
	}
	available, e := m.Host.HasRegisteredTool(scope, "Skill")
	if e != nil {
		return nil, e
	}
	if !available {
		return out, nil
	}
	definitions, _, e := m.nativeCommandDefinitions(context.Background(), scope)
	if e != nil {
		return nil, e
	}
	commands := []protocol.JSONObject{}
	for _, definition := range definitions {
		metadata := definition.Metadata()
		if skillcommands.Listed(metadata) {
			commands = append(commands, metadata)
		}
	}
	m.ensureNativeSkillState()
	key := m.options.AgentID
	var call *builtinwrite.CallContext
	if len(toolContexts) > 0 && toolContexts[0] != nil {
		call = toolContexts[0]
		key = call.AgentID
	}
	sent := m.skillListing.sent[key]
	if sent == nil {
		sent = map[string]bool{}
		m.skillListing.sent[key] = sent
	}
	suppress, e := m.skillListing.consumeSuppression(scope)
	if e != nil {
		return nil, e
	}
	if suppress {
		for _, command := range commands {
			sent[command.String("name")] = true
		}
		return out, nil
	}
	newSkills := []protocol.JSONObject{}
	for _, command := range commands {
		if !sent[command.String("name")] {
			newSkills = append(newSkills, command)
		}
	}
	if len(newSkills) == 0 {
		return out, nil
	}
	initial := len(sent) == 0
	for _, command := range newSkills {
		sent[command.String("name")] = true
	}
	var window float64
	if call == nil {
		window, e = m.Host.InferenceContextWindow(scope)
	} else {
		window, e = m.Host.ContextWindowForModel(scope, call.MainLoopModel)
	}
	if e != nil {
		return nil, e
	}
	content, e := skillcommands.FormatWithinBudget(newSkills, window, m.options.Native.Environment["SLASH_COMMAND_TOOL_CHAR_BUDGET"])
	if e != nil {
		return nil, e
	}
	out = append(out, queryloop.Message{Record: m.factory.CreateAttachment(rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("skill_listing")}, protocol.JSONField{Name: "content", Value: content}, protocol.JSONField{Name: "skillCount", Value: rawMain(len(newSkills))}, protocol.JSONField{Name: "isInitial", Value: rawMain(initial)})))})
	return out, nil
}
