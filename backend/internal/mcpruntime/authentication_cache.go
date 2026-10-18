package mcpruntime

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const authenticationFailureTTL = 15 * 60 * 1000

type AuthenticationCacheOptions struct {
	Path       string
	NativePath func() string
	ReadFile   func(string) ([]byte, error)
	WriteFile  func(string, []byte) error
	MkdirAll   func(string) error
	Remove     func(string) error
	NowMillis  func() float64
}

type authenticationCacheRead struct {
	done chan struct{}
	data json.RawMessage
}

// AuthenticationCache is Source's process-graph-owned read promise and write
// chain. Reads are shared inside a discovery batch; every new batch first joins
// pending writes and observes another process's current file. It is distinct
// from OAuth credentials and never makes an explicit reconnect skip its probe.
type AuthenticationCache struct {
	options AuthenticationCacheOptions
	mu      sync.Mutex
	writes  <-chan struct{}
	read    *authenticationCacheRead
}

func NewAuthenticationCache(options AuthenticationCacheOptions) (*AuthenticationCache, error) {
	if !filepath.IsAbs(options.Path) || options.ReadFile == nil || options.WriteFile == nil || options.MkdirAll == nil || options.Remove == nil || options.NowMillis == nil {
		return nil, errors.New("MCP authentication cache requires actual private path, filesystem and clock producers")
	}
	done := make(chan struct{})
	close(done)
	return &AuthenticationCache{options: options, writes: done}, nil
}

func (c *AuthenticationCache) path() string {
	if c.options.NativePath != nil {
		return c.options.NativePath()
	}
	return c.options.Path
}

func (c *AuthenticationCache) readFile(path string) json.RawMessage {
	data, err := c.options.ReadFile(path)
	if err != nil {
		return []byte(`{}`)
	}
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON([]byte(protocol.DecodeJavaScriptUTF8(data)))
	if err != nil {
		return []byte(`{}`)
	}
	return normalized
}
func (c *AuthenticationCache) invalidate() { c.mu.Lock(); c.read = nil; c.mu.Unlock() }
func (c *AuthenticationCache) Join(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	done := c.writes
	c.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func (c *AuthenticationCache) BeginBatch(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if err := c.Join(ctx); err != nil {
		return err
	}
	c.invalidate()
	return nil
}
func (c *AuthenticationCache) readData(ctx context.Context) (json.RawMessage, error) {
	c.mu.Lock()
	read := c.read
	if read == nil {
		// The shared read promise captures getMcpAuthCachePath when created;
		// a directory change cannot redirect an already admitted file read.
		path := c.path()
		read = &authenticationCacheRead{done: make(chan struct{})}
		c.read = read
		go func() { read.data = c.readFile(path); close(read.done) }()
	}
	c.mu.Unlock()
	select {
	case <-read.done:
		return read.data, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}
func cacheNumber(value json.RawMessage) (float64, error) {
	if len(value) == 0 {
		return math.NaN(), nil
	}
	switch string(value) {
	case "null", "false":
		return 0, nil
	case "true":
		return 1, nil
	}
	var number float64
	if json.Unmarshal(value, &number) == nil {
		return number, nil
	}
	text, err := protocol.ExactJavaScriptStringJSON(value)
	if err != nil {
		return 0, err
	}
	var stringValue string
	if err = json.Unmarshal(text, &stringValue); err != nil {
		return 0, err
	}
	stringValue = protocol.TrimJavaScriptString(stringValue)
	if stringValue == "" {
		return 0, nil
	}
	if len(stringValue) > 2 && stringValue[0] == '0' {
		base := 0
		switch stringValue[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			for _, char := range stringValue[2:] {
				valid := char >= '0' && char <= '9' && int(char-'0') < base || base == 16 && (char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F')
				if !valid {
					return math.NaN(), nil
				}
			}
			integer, ok := new(big.Int).SetString(stringValue[2:], base)
			if !ok || integer.Sign() < 0 {
				return math.NaN(), nil
			}
			number, _ := new(big.Float).SetInt(integer).Float64()
			return number, nil
		}
	}
	if stringValue == "Infinity" || stringValue == "+Infinity" {
		return math.Inf(1), nil
	}
	if stringValue == "-Infinity" {
		return math.Inf(-1), nil
	}
	// Go accepts spellings such as Inf and hexadecimal floats which the original
	// Number conversion rejects. JSON timestamps do not install callable values.
	if strings.ContainsAny(stringValue, "xXoObBpP_") || strings.Contains(stringValue, "Inf") || strings.EqualFold(stringValue, "nan") {
		return math.NaN(), nil
	}
	number, err = strconv.ParseFloat(stringValue, 64)
	if err != nil && !math.IsInf(number, 0) {
		return math.NaN(), nil
	}
	return number, nil
}
func (c *AuthenticationCache) Cached(ctx context.Context, name string) (bool, error) {
	if c == nil {
		return false, nil
	}
	data, err := c.readData(ctx)
	if err != nil {
		return false, err
	}
	if string(data) == "null" {
		return false, errors.New("Cannot read properties of null")
	}
	var cache protocol.JSONObject
	var entry json.RawMessage
	if cache.UnmarshalJSON(data) == nil && cache.IsObject() {
		entry = cache.Get(name)
	} else if len(data) > 0 && data[0] == '[' {
		var entries []json.RawMessage
		_ = json.Unmarshal(data, &entries)
		if index, err := strconv.ParseUint(name, 10, 32); err == nil && strconv.FormatUint(index, 10) == name && index < uint64(len(entries)) {
			entry = entries[index]
		}
	}
	if len(entry) == 0 || string(entry) == "null" || string(entry) == "false" || string(entry) == "0" || string(entry) == `""` {
		return false, nil
	}
	var row protocol.JSONObject
	_ = row.UnmarshalJSON(entry)
	timestamp, err := cacheNumber(row.Get("timestamp"))
	if err != nil {
		return false, err
	}
	return c.options.NowMillis()-timestamp < authenticationFailureTTL, nil
}
func (c *AuthenticationCache) enqueue(work func()) <-chan struct{} {
	c.mu.Lock()
	previous := c.writes
	done := make(chan struct{})
	c.writes = done
	c.mu.Unlock()
	go func() { defer close(done); <-previous; work() }()
	return done
}
func (c *AuthenticationCache) Mark(name string) {
	if c == nil {
		return
	}
	path := c.path()
	c.enqueue(func() {
		var cache protocol.JSONObject
		if cache.UnmarshalJSON(c.readFile(path)) != nil || !cache.IsObject() {
			return
		}
		// A direct assignment to an inherited __proto__ setter creates no JSON
		// own key. An existing own key parsed from disk remains an ordinary slot.
		if name == "__proto__" && cache.Get(name) == nil {
			return
		}
		timestamp, _ := json.Marshal(c.options.NowMillis())
		if len(timestamp) == 0 {
			timestamp = []byte(`null`)
		}
		row, err := json.Marshal(protocol.NewJSONObject(protocol.JSONField{Name: "timestamp", Value: timestamp}))
		if err != nil {
			return
		}
		cache.Set(name, row)
		data, err := cache.MarshalJSON()
		if err != nil {
			return
		}
		if c.options.MkdirAll(filepath.Dir(path)) != nil {
			return
		}
		if c.options.WriteFile(path, data) == nil {
			c.invalidate()
		}
	})
}

// A nil name is Source's clear-all operation. Both variants are best effort,
// and targeted clears join every earlier 401 write before reading the file.
func (c *AuthenticationCache) queueClear(name *string) <-chan struct{} {
	if c == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	path := c.path()
	c.invalidate()
	var target string
	if name != nil {
		target = *name
	}
	done := c.enqueue(func() {
		defer c.invalidate()
		if name == nil {
			_ = c.options.Remove(path)
			return
		}
		var cache protocol.JSONObject
		if cache.UnmarshalJSON(c.readFile(path)) != nil || !cache.IsObject() || cache.Get(target) == nil {
			return
		}
		cache.Delete(target)
		if data, err := cache.MarshalJSON(); err == nil {
			_ = c.options.WriteFile(path, data)
		}
	})
	return done
}
func (c *AuthenticationCache) Clear(ctx context.Context, name *string) error {
	done := c.queueClear(name)
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
