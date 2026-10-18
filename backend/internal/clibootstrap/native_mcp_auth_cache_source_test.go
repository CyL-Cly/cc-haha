package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceMCPAuthenticationBackoffBatchTTLDiscoveryAndRealProbes(t *testing.T) {
	const now = 1791244800000
	for _, tc := range []struct {
		name, entry          string
		anonymous, discovery bool
		status               string
		skip                 bool
	}{
		{"recent", `{"timestamp":1791244800000}`, true, false, "needs-auth", true},
		{"within-ttl", `{"timestamp":1791243900001}`, true, false, "needs-auth", true},
		{"exact-ttl-expires", `{"timestamp":1791243900000}`, true, false, "connected", false},
		{"future", `{"timestamp":1791244801000}`, true, false, "needs-auth", true},
		{"numeric-string", `{"timestamp":"1791244800000"}`, true, false, "needs-auth", true},
		{"single-array-number", `{"timestamp":[1791244800000]}`, true, false, "needs-auth", true},
		{"invalid-timestamp", `{"timestamp":"not-a-time"}`, true, false, "connected", false},
		{"null-timestamp", `{"timestamp":null}`, true, false, "connected", false},
		{"missing-timestamp", `{}`, true, false, "connected", false},
		{"expired-discovery-no-token", `{"timestamp":1791243800000}`, true, true, "needs-auth", true},
		{"actual-401-records-failure", `{"timestamp":1791243800000}`, false, false, "needs-auth", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := `{"auth":` + tc.entry + `,"other":{"timestamp":1791244800000,"future":"keep"}}`
			credentials := `{"future":{"keep":"private-auth-fixture"}}`
			x := newTerminalMCPAuthFixtureWithOptions(t, terminalMCPAuthFixtureOptions{headless: true, allowAnonymous: tc.anonymous, prepare: func(x *terminalMCPAuthFixture) {
				x.f.n.Now = func() time.Time { return time.UnixMilli(now) }
				x.f.n.NowMillis = func() float64 { return now }
				x.f.write(t, filepath.Join(x.f.config, "mcp-needs-auth-cache.json"), cache)
				if tc.discovery {
					key, err := mcpauth.GetServerKey("auth", mainObj(rawMain(map[string]any{"type": "http", "url": x.peer.URL + "/mcp"})))
					if err != nil {
						t.Fatal(err)
					}
					credentials = string(rawMain(map[string]any{"future": map[string]any{"keep": "private-auth-fixture"}, "mcpOAuth": map[string]any{key: map[string]any{"accessToken": "", "clientId": "retained-client", "future": "keep", "discoveryState": map[string]any{"authorizationServerUrl": x.peer.URL}}}}))
					x.f.write(t, filepath.Join(x.f.config, ".credentials.json"), credentials)
				}
			}})
			goProbes := x.mcpAttempts.Load()
			output := runActualMCPAuthenticationSourceWithInput(t, x, "cache-probe", map[string]any{"now": now, "cacheRaw": cache, "credentialsRaw": credentials})
			var source struct {
				State struct {
					Clients []struct{ Name, Type string }
					Tools   []string
				}
				CacheRaw *string
			}
			if err := json.Unmarshal(output, &source); err != nil {
				t.Fatal(err, string(output))
			}
			if len(source.State.Clients) != 1 || source.State.Clients[0].Type != tc.status {
				t.Fatal("actual Source status", tc.status, string(output))
			}
			sourceProbes := x.mcpAttempts.Load() - goProbes
			if (goProbes == 0) != tc.skip || (sourceProbes == 0) != tc.skip {
				t.Fatal("cached or discovery shortcut performed a real MCP probe", goProbes, sourceProbes, tc.skip)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := x.m.mcpRuntime.BeginDiscoveryBatch(ctx); err != nil {
				t.Fatal(err)
			}
			ownCache, err := os.ReadFile(filepath.Join(x.f.config, "mcp-needs-auth-cache.json"))
			if err != nil {
				t.Fatal(err)
			}
			if source.CacheRaw == nil {
				t.Fatal("original cache unexpectedly absent", string(output))
			}
			expected, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(*source.CacheRaw))
			if err != nil {
				t.Fatal(err)
			}
			observed, err := protocol.ParseAndStringifyJavaScriptJSON(ownCache)
			if err != nil || !bytes.Equal(expected, observed) {
				t.Fatal("actual cache writes differ from Source", string(expected), string(observed), err)
			}
			if err := x.m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				rows, err := x.m.mcpTools.Status(scope)
				if err != nil {
					return err
				}
				if len(rows) != 1 || rows[0].String("status") != tc.status {
					return fmt.Errorf("native status differs from Source: %v", rows)
				}
				_, tools, err := x.m.mcpTools.Catalog(scope)
				if err != nil {
					return err
				}
				names := []string{}
				for _, tool := range tools {
					names = append(names, tool.Name)
				}
				if !bytes.Equal(rawMain(names), rawMain(source.State.Tools)) {
					return fmt.Errorf("actual auth/real tool catalog differs: %s vs %s", rawMain(names), rawMain(source.State.Tools))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if x.tokens.Load() != 0 || x.models.Load() != 0 {
				t.Fatal("batch probe unexpectedly used model/consent quota")
			}
			t.Logf("Source/Go status=%s sourceProbes=%d goProbes=%d cache=%s", tc.status, sourceProbes, goProbes, observed)
		})
	}
}

func TestActualSourceMCPAuthenticationCacheExternalClearExplicitReconnectAndCredentialSupersession(t *testing.T) {
	const now = 1791244800000
	for _, mode := range []string{"external-clear", "explicit-success-clears-discovery", "explicit-failure-removes-pseudo"} {
		t.Run(mode, func(t *testing.T) {
			cache := `{"auth":{"timestamp":1791244800000},"other":{"timestamp":1791244800000,"future":"keep"}}`
			nextCache := `{"other":{"timestamp":1791244800000,"future":"keep"}}`
			credentials := `{"future":{"keep":"private-auth-fixture"}}`
			key := ""
			x := newTerminalMCPAuthFixtureWithOptions(t, terminalMCPAuthFixtureOptions{headless: true, allowAnonymous: mode != "explicit-failure-removes-pseudo", prepare: func(x *terminalMCPAuthFixture) {
				x.f.n.Now = func() time.Time { return time.UnixMilli(now) }
				x.f.n.NowMillis = func() float64 { return now }
				x.f.write(t, filepath.Join(x.f.config, "mcp-needs-auth-cache.json"), cache)
				if mode == "explicit-success-clears-discovery" {
					var err error
					key, err = mcpauth.GetServerKey("auth", mainObj(rawMain(map[string]any{"type": "http", "url": x.peer.URL + "/mcp"})))
					if err != nil {
						t.Fatal(err)
					}
					credentials = string(rawMain(map[string]any{"future": map[string]any{"keep": "private-auth-fixture"}, "mcpOAuth": map[string]any{key: map[string]any{"accessToken": "", "clientId": "preserve-registration", "clientSecret": "private-retained-secret", "future": "keep", "discoveryState": map[string]any{"authorizationServerUrl": x.peer.URL}}, "other-identity": map[string]any{"discoveryState": map[string]any{"keep": true}}}}))
					x.f.write(t, filepath.Join(x.f.config, ".credentials.json"), credentials)
				}
			}})
			if x.mcpAttempts.Load() != 0 {
				t.Fatal("initial cache did not skip actual network", x.mcpAttempts.Load())
			}
			followup := "explicit-reconnect"
			if mode == "external-clear" {
				followup = mode
			}
			output := runActualMCPAuthenticationSourceWithInput(t, x, "cache-probe", map[string]any{"now": now, "cacheRaw": cache, "credentialsRaw": credentials, "followup": followup, "nextCacheRaw": nextCache})
			var source struct {
				Followup struct {
					Type  string
					Tools []string
				}
				NextCacheRaw       *string
				NextCredentialsRaw string
			}
			if err := json.Unmarshal(output, &source); err != nil {
				t.Fatal(err, string(output))
			}
			sourceProbes := x.mcpAttempts.Load()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if mode == "external-clear" {
				x.f.write(t, filepath.Join(x.f.config, "mcp-needs-auth-cache.json"), nextCache)
				if err := x.m.Owner.Run(ctx, func(scope *cooperative.Scope) error { return x.m.mcpTools.Discover(scope) }); err != nil {
					t.Fatal(err)
				}
			} else {
				err := x.m.TerminalMCPControl(ctx, "auth", nil)
				if (err != nil) != (mode == "explicit-failure-removes-pseudo") {
					t.Fatal("actual reconnect rejection differs from Source SDK status boundary", err)
				}
			}
			if err := x.m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
				rows, err := x.m.mcpTools.Status(scope)
				if err != nil {
					return err
				}
				if len(rows) != 1 || rows[0].String("status") != source.Followup.Type {
					return fmt.Errorf("real next status differs from Source: %v", rows)
				}
				_, tools, err := x.m.mcpTools.Catalog(scope)
				if err != nil {
					return err
				}
				names := []string{}
				for _, tool := range tools {
					names = append(names, tool.Name)
				}
				if !bytes.Equal(rawMain(names), rawMain(source.Followup.Tools)) {
					return fmt.Errorf("reconnect/batch catalog differs: %s vs %s", rawMain(names), rawMain(source.Followup.Tools))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := x.m.mcpRuntime.BeginDiscoveryBatch(ctx); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(x.f.config, "mcp-needs-auth-cache.json"))
			if err != nil {
				t.Fatal(err)
			}
			if source.NextCacheRaw == nil {
				t.Fatal("Source unexpectedly deleted entire backoff file", string(output))
			}
			expected, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(*source.NextCacheRaw))
			if err != nil {
				t.Fatal(err)
			}
			observed, err := protocol.ParseAndStringifyJavaScriptJSON(data)
			if err != nil || !bytes.Equal(expected, observed) {
				t.Fatal("actual targeted clear differs from Source", string(expected), string(observed), err)
			}
			if mode == "explicit-success-clears-discovery" {
				ownData, err := os.ReadFile(filepath.Join(x.f.config, ".credentials.json"))
				if err != nil {
					t.Fatal(err)
				}
				for _, data := range []string{source.NextCredentialsRaw, string(ownData)} {
					entry := mainObj(mainObj(mainObj([]byte(data)).Get("mcpOAuth")).Get(key))
					if entry.Get("discoveryState") != nil || entry.String("clientId") != "preserve-registration" || entry.String("clientSecret") != "private-retained-secret" || entry.String("future") != "keep" || mainObj(mainObj([]byte(data)).Get("mcpOAuth")).Get("other-identity") == nil {
						t.Fatal("successful anonymous probe damaged registration/other credentials or retained failed discovery", data)
					}
				}
			}
			goProbes := x.mcpAttempts.Load() - sourceProbes
			if goProbes == 0 || sourceProbes == 0 || x.tokens.Load() != 0 || x.models.Load() != 0 {
				t.Fatal("explicit actual connection bypassed proof or used OAuth/model quota", goProbes, sourceProbes, x.tokens.Load(), x.models.Load())
			}
			t.Logf("actual Source/Go followup=%s SourceProbes=%d GoProbes=%d cache=%s", source.Followup.Type, sourceProbes, goProbes, observed)
		})
	}
}
