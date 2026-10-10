package mcpcli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpclient"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

// ClaudeAICatalog preserves the original session-lifetime memo, including an
// empty result after a failed fetch. Its caller resolves actual token precedence.
type ClaudeAICatalog struct {
	mu          sync.Mutex
	entry       *claudeAICatalogEntry
	active      map[*claudeAICatalogEntry]struct{}
	closed      bool
	workers     sync.WaitGroup
	HTTP        *http.Client
	BaseURL     string
	Tokens      func(context.Context) (json.RawMessage, error)
	Environment map[string]string
	Bare        bool
}

type claudeAICatalogEntry struct {
	done   chan struct{}
	value  protocol.JSONObject
	cancel context.CancelFunc
}

// ClaudeAICatalogFuture pins one original memoized Promise identity. Clear
// detaches the memo without redirecting an already-admitted consumer.
type ClaudeAICatalogFuture struct{ entry *claudeAICatalogEntry }

// Begin publishes and kicks off the physical producer before the caller starts
// its local/plugin configuration work. The first caller supplies its lifetime;
// subsequent callers share the same flight without replacing its cancellation.
func (c *ClaudeAICatalog) Begin(ctx context.Context) *ClaudeAICatalogFuture {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		entry := &claudeAICatalogEntry{done: make(chan struct{}), value: protocol.NewJSONObject()}
		close(entry.done)
		return &ClaudeAICatalogFuture{entry: entry}
	}
	if c.entry != nil {
		future := &ClaudeAICatalogFuture{entry: c.entry}
		c.mu.Unlock()
		return future
	}
	producer, cancel := context.WithCancel(ctx)
	entry := &claudeAICatalogEntry{done: make(chan struct{}), cancel: cancel}
	if c.active == nil {
		c.active = map[*claudeAICatalogEntry]struct{}{}
	}
	c.active[entry] = struct{}{}
	c.entry = entry
	c.workers.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		value := c.fetch(producer)
		cancel()
		entry.value = value.Clone()
		close(entry.done)
		c.mu.Lock()
		delete(c.active, entry)
		c.mu.Unlock()
	}()
	return &ClaudeAICatalogFuture{entry: entry}
}

// Await never cancels the shared producer when only this waiter leaves. A
// completed Source memo remains readable and each caller gets an owned clone.
func (future *ClaudeAICatalogFuture) Await(ctx context.Context) protocol.JSONObject {
	if future == nil || future.entry == nil {
		return protocol.NewJSONObject()
	}
	entry := future.entry
	select {
	case <-entry.done:
		return entry.value.Clone()
	default:
	}
	select {
	case <-entry.done:
		return entry.value.Clone()
	case <-ctx.Done():
		return protocol.NewJSONObject()
	}
}
func (c *ClaudeAICatalog) Configurations(ctx context.Context) protocol.JSONObject {
	return c.Begin(ctx).Await(ctx)
}

func (c *ClaudeAICatalog) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Clearing an in-flight memo must not make its producer settle a new entry.
	c.entry = nil
}

// Close is an off-owner resource join. Owner callers admit it through their
// existing native-work future, so credential Read completions can still run.
func (c *ClaudeAICatalog) Close() {
	c.mu.Lock()
	c.closed = true
	entries := make([]*claudeAICatalogEntry, 0, len(c.active))
	for entry := range c.active {
		entries = append(entries, entry)
	}
	c.mu.Unlock()
	for _, entry := range entries {
		entry.cancel()
	}
	c.workers.Wait()
}

func (c *ClaudeAICatalog) fetch(ctx context.Context) protocol.JSONObject {
	empty := protocol.NewJSONObject()
	if c.HTTP == nil || c.Tokens == nil {
		return empty
	}
	needs, e := ClaudeAIRequiresSecure(c.Environment, c.Bare)
	if e != nil || !needs {
		return empty
	}
	secure, err := c.Tokens(ctx)
	if err != nil {
		return empty
	}
	eligible, err := ClaudeAIEligibility(c.Environment, c.Bare, secure)
	if err != nil || !eligible {
		return empty
	}
	var root protocol.JSONObject
	if root.UnmarshalJSON(secure) != nil {
		return empty
	}
	tokens, ok := root.Object("claudeAiOauth")
	if !ok {
		return empty
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/v1/mcp_servers?limit=1000", nil)
	if err != nil {
		return empty
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("User-Agent", "axios/1.14.0")
	request.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	request.Header.Set("Authorization", "Bearer "+cliauth.OAuthFieldString(tokens.Get("accessToken")))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("anthropic-beta", "mcp-servers-2025-12-04")
	request.Header.Set("anthropic-version", "2023-06-01")
	response, err := c.HTTP.Do(request)
	if err != nil {
		return empty
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return empty
	}
	body, err := webfetch.ReadAxiosResponseBody(response)
	if err != nil {
		return empty
	}
	var entity protocol.JSONObject
	if entity.UnmarshalJSON(body) != nil {
		return empty
	}
	var rows []json.RawMessage
	if json.Unmarshal(entity.Get("data"), &rows) != nil || string(entity.Get("data")) == "null" {
		return empty
	}
	used := map[string]bool{}
	result := protocol.NewJSONObject()
	for _, row := range rows {
		if string(row) == "null" {
			return empty
		}
		var server protocol.JSONObject
		// Property lookup on non-null primitive values produces undefined.
		_ = server.UnmarshalJSON(row)
		display, err := protocol.ExactJavaScriptStringJSON(server.Get("display_name"))
		if err != nil {
			return empty
		}
		units, err := protocol.DecodeJavaScriptJSONString(display)
		if err != nil {
			return empty
		}
		base := append(utf16.Encode([]rune("claude.ai ")), units...)
		name := append([]uint16(nil), base...)
		// Name normalization replaces every non-ASCII UTF-16 unit. Decoding only
		// for that normalization retains its result; the property itself stays raw.
		normalized := func() string { return mcpclient.NormalizeName(string(utf16.Decode(name))) }
		for count := 2; used[normalized()]; count++ {
			encoded, _ := json.Marshal(count)
			name = append(append([]uint16(nil), base...), utf16.Encode([]rune(" ("+string(encoded)+")"))...)
		}
		used[normalized()] = true
		config := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"claudeai-proxy"`)})
		config.Set("url", server.Get("url"))
		config.Set("id", server.Get("id"))
		config.Set("scope", json.RawMessage(`"claudeai"`))
		encoded, _ := config.MarshalJSON()
		_ = result.SetPropertyJSON(protocol.QuoteJavaScriptUTF16(name), encoded)
	}
	return result
}

// Keep an explicit session binding; the proxy URL does not use server.url.
func ClaudeAIProxyAddress(base, path string, id json.RawMessage) string {
	return base + strings.Replace(path, "{server_id}", cliauth.OAuthFieldString(id), 1)
}
