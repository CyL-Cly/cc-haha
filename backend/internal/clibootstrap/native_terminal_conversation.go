package clibootstrap

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/preflightcore"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type TerminalConversationSnapshot struct {
	Messages                   []protocol.JSONObject
	Metadata                   protocol.JSONObject
	CWD, Temp                  string
	Now                        time.Time
	Teammate, CopyFullResponse bool
}

// Source exportRenderer creates a new default AppState, so its header resolves
// the default model rather than the enclosing REPL's temporary model override.
// Settings, cached credential identity and cwd remain owned by this process.
func (m *MainPrint) TerminalExportPresentation(ctx context.Context) (tui.ConversationExportPresentation, error) {
	var result tui.ConversationExportPresentation
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.Host == nil {
			return cooperative.ErrClosed
		}
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		settings, err := m.Startup.GetInitialSettings()
		if err != nil {
			return err
		}
		selector, err := m.nativeModelSelection(ctx, scope, nativeModelOptions(settings, m.nativeOpenAIEnvironment(), ""))
		if err != nil {
			return err
		}
		resolved, err := selector.Parse(selector.DefaultSetting())
		if err != nil {
			return err
		}
		model, err := decodeNativeModel(resolved)
		if err != nil {
			return err
		}
		label, err := selector.PublicModelDisplayName(quoteMain(model))
		if err != nil {
			return err
		}
		if label == "" {
			label = model
		}
		identity, err := m.openAIAuth.AnthropicModelIdentity(ctx, scope, !m.options.Interactive)
		if err != nil {
			return err
		}
		billing := "API Usage Billing"
		if identity.Subscriber {
			billing = "Claude API"
			for _, pair := range [][2]string{{"enterprise", "Claude Enterprise"}, {"team", "Claude Team"}, {"max", "Claude Max"}, {"pro", "Claude Pro"}} {
				if identity.SubscriptionType == pair[0] {
					billing = pair[1]
				}
			}
		}
		cwd, version := m.workingDirectory(), m.options.Version
		if demo := m.options.Native.Environment["DEMO_VERSION"]; demo != "" {
			version, cwd = demo, "/code/claude"
		} else {
			home := m.options.Native.Home
			if strings.HasPrefix(cwd, home+string(os.PathSeparator)) {
				cwd = "~" + strings.TrimPrefix(cwd, home)
			}
		}
		welcome := "Welcome back!"
		if global != nil {
			if account, ok := global.Object("oauthAccount"); ok {
				name := account.String("displayName")
				if name != "" && len(utf16.Encode([]rune(name))) <= 20 {
					welcome = "Welcome back " + name + "!"
				}
			}
		}
		agent := ""
		if settings != nil {
			agent = settings.String("agent")
		}
		result = tui.ConversationExportPresentation{Version: version, WorkingDirectory: cwd, Model: label, BillingType: billing, Welcome: welcome, Agent: agent}
		return m.populateTerminalExportLogo(ctx, scope, global, identity.Subscriber, identity.SubscriptionType, &result)
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}

func (m *MainPrint) TerminalConversation(ctx context.Context) (TerminalConversationSnapshot, error) {
	var result TerminalConversationSnapshot
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.Host == nil {
			return cooperative.ErrClosed
		}
		metadata, err := m.Controller.TerminalMetadataOnOwner(scope)
		if err != nil {
			return err
		}
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		full := global != nil && string(global.Get("copyFullResponse")) == "true"
		messages, err := querypreflight.InspectionRecords(m.Host.History())
		if err != nil {
			return err
		}
		for index, message := range messages {
			messages[index] = message.Clone()
		}
		result = TerminalConversationSnapshot{Messages: messages, Metadata: metadata, CWD: m.workingDirectory(), Temp: m.options.Native.Temp, Now: m.options.Native.Now(), Teammate: m.teamWorker != nil, CopyFullResponse: full}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}

func (m *MainPrint) SaveTerminalConversationMetadata(ctx context.Context, kind string, value json.RawMessage) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if m.closed {
			return cooperative.ErrClosed
		}
		return m.Controller.SaveTerminalMetadataJSONOnOwner(scope, kind, value)
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}

const terminalConversationNameSystem = `Generate a short kebab-case name (2-4 words) that captures the main topic of this conversation. Use lowercase words separated by hyphens. Examples: "fix-login-bug", "add-auth-feature", "refactor-api-client", "debug-test-failures". Return JSON with a "name" field.`

func TerminalConversationNameInput(messages []protocol.JSONObject) (json.RawMessage, error) {
	projection, err := preflightcore.AfterCompactBoundary(messages)
	if err != nil {
		return nil, err
	}
	var parts [][]uint16
	for _, record := range projection.Records {
		message := record.Record
		if (message.String("type") != "assistant" && message.String("type") != "user") || string(message.Get("isMeta")) == "true" {
			continue
		}
		if origin, ok := message.Object("origin"); ok && origin.String("kind") != "human" {
			continue
		}
		envelope, ok := message.Object("message")
		if !ok {
			continue
		}
		content := envelope.Get("content")
		if units, e := protocol.DecodeJavaScriptJSONString(content); e == nil {
			parts = append(parts, units)
			continue
		}
		var blocks []protocol.JSONObject
		if json.Unmarshal(content, &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if block.String("type") == "text" {
				units, e := protocol.DecodeJavaScriptJSONString(block.Get("text"))
				if e != nil {
					return nil, e
				}
				parts = append(parts, units)
			}
		}
	}
	var units []uint16
	for index, part := range parts {
		if index > 0 {
			units = append(units, '\n')
		}
		units = append(units, part...)
	}
	if len(units) > 1000 {
		units = units[len(units)-1000:]
	}
	if len(units) == 0 {
		return nil, nil
	}
	return protocol.QuoteJavaScriptUTF16(units), nil
}

func (m *MainPrint) GenerateTerminalConversationName(ctx context.Context) (json.RawMessage, error) {
	var name json.RawMessage
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if m.closed || m.Host == nil {
			return cooperative.ErrClosed
		}
		messages, err := querypreflight.InspectionRecords(m.Host.History())
		if err != nil {
			return err
		}
		prompt, err := TerminalConversationNameInput(messages)
		if err != nil || prompt == nil {
			return err
		}
		text, err := m.Host.QueryTerminalConversationName(ctx, scope, prompt, []json.RawMessage{quoteMain(terminalConversationNameSystem)}, m.nativeModelSelector.SmallFastModel())
		if err != nil {
			return nil
		} // Source converts expected model failures to no name.
		var parsed protocol.JSONObject
		if parsed.UnmarshalJSON([]byte(strings.TrimPrefix(text, "\ufeff"))) != nil {
			return nil
		}
		if _, err := protocol.DecodeJavaScriptJSONString(parsed.Get("name")); err == nil {
			name = append(json.RawMessage(nil), parsed.Get("name")...)
		}
		return nil
	})
	return name, err
}
