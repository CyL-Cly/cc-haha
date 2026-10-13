package sdkhost

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
)

// PrintRegistry owns the migrated normal metadata declarations and performs
// native project discovery. It is a partial command catalog: full init parity
// stays unaccepted until remaining builtin/bundled/dynamic guards are migrated.
// Captured Source output is never a constructor input.
type PrintRegistry struct {
	catalog               PrintCatalog
	commands              []printCommandDefinition
	environment           func(string) string
	discoveryErrors       []string
	prompts               []skillcommands.Definition
	nativePrompts         bool
	slashCommandsDisabled bool
}

// AgentGuideCommands carries the real prompt/local discriminant and Source
// origin; the UI control projection intentionally omits those fields.
func (s *PrintRegistry) AgentGuideCommands() []protocol.JSONObject {
	out := []protocol.JSONObject{}
	if s.slashCommandsDisabled {
		return out
	}
	for _, command := range s.commands {
		record := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote(command.kind)}, protocol.JSONField{Name: "name", Value: commandStringJSON(command.rawName, command.name)}, protocol.JSONField{Name: "description", Value: commandStringJSON(command.rawDescription, command.description)})
		if command.source != "" {
			record.Set("source", quote(command.source))
		}
		out = append(out, record)
	}
	return out
}

type PrintCatalog struct {
	Agents    []string
	Commands  []protocolCommand
	Skills    []string
	rawSkills []json.RawMessage
	Plugins   []json.RawMessage
}
type protocolCommand struct {
	Name                    string          `json:"name"`
	Description             string          `json:"description"`
	ArgumentHint            string          `json:"argumentHint,omitempty"`
	rawName, rawDescription json.RawMessage `json:"-"`
}
type PrintRegistryOptions struct {
	Interactive            bool
	DisableSlashCommands   bool
	CWD, Home              string
	Stat                   func(string) (fs.FileInfo, error)
	ReadDir                func(string) ([]fs.DirEntry, error)
	EntryPoint, UserType   string
	BuiltinAgentsDisabled  bool
	Environment            func(string) string
	NativePrompts          *[]skillcommands.Definition
	Plugins                []json.RawMessage
	NativeAgents           *[]string
	NativeWorkflowCommands *[]skillcommands.Definition
}

func NewPrintRegistry(o PrintRegistryOptions) (*PrintRegistry, error) {
	if o.CWD == "" || o.Home == "" || o.Stat == nil || o.ReadDir == nil || o.EntryPoint != "sdk-cli" || o.UserType != "external" {
		return nil, cap("normal registry native/profile binding")
	}
	c := PrintCatalog{Agents: []string{}, Commands: []protocolCommand{}, Skills: []string{}, Plugins: []json.RawMessage{}}
	if !o.BuiltinAgentsDisabled {
		c.Agents = []string{"general-purpose", "statusline-setup", "Explore", "Plan", "verification"}
	}
	if o.NativeAgents != nil {
		c.Agents = append([]string{}, (*o.NativeAgents)...)
	}
	// Shipped Source declarations are filtered at the initial main host boundary.
	commands := filterEnabledPrintCommands(migratedPrintCommands(), o.Environment)
	if !o.Interactive {
		commands = filterHeadlessCommands(commands)
	}
	c.Commands = projectSystemCommands(commands)
	s := &PrintRegistry{catalog: c, commands: commands, environment: o.Environment, slashCommandsDisabled: o.DisableSlashCommands}
	if o.NativePrompts != nil {
		s.nativePrompts = true
		s.ReplaceNativePrompts(*o.NativePrompts, o.Plugins, !o.Interactive)
	}
	// The original project walk runs even when --setting-sources is empty.
	// A generic guard error is not an inaccessible errno. Stop this discovery
	// chain at its original outer fallback, rather than manufacturing ENOENT.
	for _, kind := range []string{"skills", "workflows", "agents"} {
		if kind == "workflows" && o.NativeWorkflowCommands != nil {
			continue
		}
		if kind == "agents" && o.NativeAgents != nil {
			continue
		}
		if kind == "skills" && o.NativePrompts != nil {
			continue
		}
		for d := o.CWD; ; d = filepath.Dir(d) {
			p := filepath.Join(d, ".claude", kind)
			info, e := o.Stat(p)
			if e != nil && !isInaccessible(e) {
				s.discoveryErrors = append(s.discoveryErrors, e.Error())
				break
			}
			if e == nil && info.IsDir() {
				entries, re := o.ReadDir(p)
				if re != nil {
					s.discoveryErrors = append(s.discoveryErrors, re.Error())
					break
				}
				if len(entries) > 0 {
					return nil, cap("active normal " + kind + " discovery")
				}
			}
			if kind == "skills" {
				info, e = o.Stat(filepath.Join(d, ".agents", "skills"))
				if e != nil && !isInaccessible(e) {
					s.discoveryErrors = append(s.discoveryErrors, e.Error())
					break
				}
				if e == nil && info.IsDir() {
					entries, re := o.ReadDir(filepath.Join(d, ".agents", "skills"))
					if re != nil {
						return nil, re
					}
					if len(entries) > 0 {
						return nil, cap("active .agents skills")
					}
				}
			}
			if d == o.Home || filepath.Dir(d) == d {
				break
			}
		}
	}
	return s, nil
}

func (s *PrintRegistry) ReplaceNativeAgents(names []string) {
	s.catalog.Agents = append([]string{}, names...)
}
func isInaccessible(e error) bool {
	return errors.Is(e, fs.ErrNotExist) || errors.Is(e, fs.ErrPermission) || errors.Is(e, syscall.ENOTDIR) || errors.Is(e, syscall.ELOOP)
}
func (s *PrintRegistry) Catalog() PrintCatalog {
	c := s.catalog
	c.Agents = append([]string{}, c.Agents...)
	c.Commands = append([]protocolCommand{}, c.Commands...)
	c.Skills = append([]string{}, c.Skills...)
	if c.rawSkills != nil {
		c.rawSkills = make([]json.RawMessage, len(c.rawSkills))
		for i, value := range s.catalog.rawSkills {
			c.rawSkills[i] = append(json.RawMessage(nil), value...)
		}
	}
	c.Plugins = append([]json.RawMessage{}, c.Plugins...)
	if s.slashCommandsDisabled {
		c.Commands = []protocolCommand{}
		c.Skills = []string{}
		c.rawSkills = []json.RawMessage{}
	}
	return c
}
func (s *PrintRegistry) DiscoveryErrors() []string    { return append([]string{}, s.discoveryErrors...) }
func (s *PrintRegistry) FullInitParityAccepted() bool { return false }

func (s *PrintRegistry) HasNativePluginBindings() bool { return s != nil && s.nativePrompts }

// Host consumes the original quoted identity when the native producer has it.
// Literal catalogs retain their existing []string surface and JSON behavior.
func (c PrintCatalog) skillNamesJSON() json.RawMessage {
	if c.rawSkills != nil {
		return raw(c.rawSkills)
	}
	return raw(c.Skills)
}
