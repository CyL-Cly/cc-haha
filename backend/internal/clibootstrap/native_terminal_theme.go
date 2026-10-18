package clibootstrap

import (
	"context"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// TerminalThemeState reads the existing stores on their owner. Unlike Config's
// model row, ThemePicker does not acquire account/model state or query a model.
func (m *MainPrint) TerminalThemeState(ctx context.Context) (protocol.JSONObject, error) {
	var value protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		initial, err := m.Startup.GetInitialSettings()
		if err != nil {
			return err
		}
		env := m.nativeOpenAIEnvironment()
		syntaxEnvironment := env["CLAUDE_CODE_SYNTAX_HIGHLIGHT"]
		normalized := strings.ToLower(strings.TrimFunc(syntaxEnvironment, protocol.IsJavaScriptWhitespace))
		reason := []byte("null")
		if normalized == "0" || normalized == "false" || normalized == "no" || normalized == "off" {
			reason = quoteMain("env")
		}
		disabled := initial != nil && string(initial.Get("syntaxHighlightingDisabled")) == "true"
		if _, present := env["CLAUDE_CODE_SYNTAX_HIGHLIGHT"]; !present {
			// Source's reason is cached at mount, but a later rerender embeds
			// the current process.env value in its disabled-by-env caption.
			syntaxEnvironment = "undefined"
		}
		value = protocol.NewJSONObject(protocol.JSONField{Name: "theme", Value: global.Get("theme")}, protocol.JSONField{Name: "syntaxHighlightingDisabled", Value: rawMain(disabled)}, protocol.JSONField{Name: "colorModuleUnavailableReason", Value: reason}, protocol.JSONField{Name: "syntaxEnvironment", Value: quoteMain(syntaxEnvironment)}, protocol.JSONField{Name: "colorEnvironment", Value: quoteMain(env["COLORTERM"])}, protocol.JSONField{Name: "autoThemeBuildFeature", Value: []byte("false")})
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return value, err
}

func (m *MainPrint) SetTerminalThemeSyntaxHighlighting(ctx context.Context, disabled bool) error {
	return m.SetTerminalConfigSetting(ctx, "syntaxHighlightingDisabled", rawMain(disabled))
}
