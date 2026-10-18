package clibootstrap

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func terminalPluginMergedErrors(previous, incoming []protocol.JSONObject) []protocol.JSONObject {
	key := func(row protocol.JSONObject) string {
		parts := []json.RawMessage{row.Get("type"), row.Get("source")}
		if row.String("type") == "generic-error" {
			parts = append(parts, row.Get("error"))
		}
		units := []uint16{}
		for i, part := range parts {
			if i > 0 {
				units = append(units, ':')
			}
			encoded, _ := protocol.JavaScriptStringJSON(part)
			value, _ := protocol.DecodeJavaScriptJSONString(encoded)
			units = append(units, value...)
		}
		return string(protocol.QuoteJavaScriptUTF16(units))
	}
	newKeys := map[string]bool{}
	for _, row := range incoming {
		newKeys[key(row)] = true
	}
	merged := []protocol.JSONObject{}
	for _, row := range previous {
		source := row.String("source")
		if (source == "lsp-manager" || strings.HasPrefix(source, "plugin:")) && !newKeys[key(row)] {
			merged = append(merged, row.Clone())
		}
	}
	for _, row := range incoming {
		merged = append(merged, row.Clone())
	}
	return merged
}

func (m *MainPrint) MarkTerminalFlaggedPluginsSeen(ctx context.Context, ids []string) error {
	encoded := make([]json.RawMessage, len(ids))
	for i, id := range ids {
		encoded[i] = quoteMain(id)
	}
	return m.MarkTerminalFlaggedPluginsSeenJSON(ctx, encoded)
}

func (m *MainPrint) MarkTerminalFlaggedPluginsSeenJSON(ctx context.Context, ids []json.RawMessage) error {
	_, err := m.terminalPluginWorker(ctx, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		p := m.nativeSettings
		p.mu.Lock()
		if err := p.ensureSettings(); err != nil {
			p.mu.Unlock()
			return protocol.JSONObject{}, err
		}
		registry := p.plugins.flagged
		p.mu.Unlock()
		return protocol.NewJSONObject(), registry.MarkSeenJSON(worker, ids, m.options.Native.Now())
	})
	return err
}

func (m *MainPrint) RemoveTerminalFlaggedPlugin(ctx context.Context, id string) error {
	return m.RemoveTerminalFlaggedPluginJSON(ctx, quoteMain(id))
}

func (m *MainPrint) RemoveTerminalFlaggedPluginJSON(ctx context.Context, id json.RawMessage) error {
	_, err := m.terminalPluginWorker(ctx, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		p := m.nativeSettings
		p.mu.Lock()
		if err := p.ensureSettings(); err != nil {
			p.mu.Unlock()
			return protocol.JSONObject{}, err
		}
		registry := p.plugins.flagged
		p.mu.Unlock()
		return protocol.NewJSONObject(), registry.RemoveJSON(worker, id)
	})
	return err
}
