package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	"github.com/NanmiCoder/cc-haha/backend/internal/websearch"
	"time"
)

func (m *MainPrint) nativeWebSearchBindings() (*builtintools.WebSearchBindings, error) {
	if !m.options.Native.NativeProcess {
		return nil, nil
	}
	n := m.options.Native
	client, e := webfetch.NewHTTPClient(n.Environment, m.tls)
	if e != nil {
		return nil, e
	}
	service := m.webSearch
	if service == nil {
		service, e = websearch.New(websearch.Options{Client: client, Now: n.Now, Settings: func() (protocol.JSONObject, error) {
			settings, e := m.Startup.GetInitialSettings()
			if e != nil {
				return protocol.JSONObject{}, e
			}
			return settings.Clone(), nil
		}})
		if e != nil {
			return nil, e
		}
		m.webSearch = service
	}
	return &builtintools.WebSearchBindings{Search: service, Model: func(scope *cooperative.Scope) (string, error) {
		if !scope.BelongsTo(m.Owner) {
			return "", cooperative.ErrRestricted
		}
		if m.Host != nil {
			return m.Host.InferenceModel(scope)
		}
		return m.options.Arguments.Model, nil
	}, Native: func(ctx context.Context, scope *cooperative.Scope, input protocol.JSONObject, model string, nonInteractive bool, progress websearch.Progress) ([]protocol.JSONObject, error) {
		if m.Host == nil {
			return nil, mainCap("original initialized WebSearch host")
		}
		return m.Host.NativeWebSearch(ctx, scope, input, model, nonInteractive, progress)
	}, MonthYear: func() string {
		now := n.Now()
		if override := n.Environment["CLAUDE_CODE_OVERRIDE_DATE"]; override != "" {
			if parsed, e := time.Parse("2006-01-02", override); e == nil {
				now = parsed
			} else if parsed, e := time.Parse(time.RFC3339, override); e == nil {
				now = parsed
			}
		}
		return now.Format("January 2006")
	}}, nil
}
