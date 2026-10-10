package securestorage

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type sourceReadFlight struct {
	done chan struct{}
	data json.RawMessage
}

func sourceJSON(data []byte) json.RawMessage {
	value, err := protocol.ParseAndStringifyJavaScriptJSON(protocol.DecodeJavaScriptUTF8(data))
	if err != nil || string(value) == "null" {
		return nil
	}
	return value
}
func rawCopy(raw json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), raw...) }
func (s *Store) cacheFresh() bool {
	return !s.cachedAt.IsZero() && s.options.Now().Sub(s.cachedAt) < 30*time.Second
}

// primarySourceJSON is called while holding the one shared secure-store lock.
// Raw values preserve the original non-null keychain preference even when an
// old credentials root is a primitive or array instead of a typed record.
func (s *Store) primarySourceJSON(ctx context.Context) json.RawMessage {
	if s.cacheFresh() {
		return rawCopy(s.cachedJSON)
	}
	output, err := s.options.RunSecurity(ctx, Command{Args: []string{"find-generic-password", "-a", s.account, "-w", "-s", s.service}})
	var next json.RawMessage
	if err == nil && len(output) > 0 {
		next = sourceJSON([]byte(protocol.TrimJavaScriptString(string(protocol.DecodeJavaScriptUTF8(output)))))
	}
	if next == nil {
		next = rawCopy(s.cachedJSON)
	}
	s.cachedJSON, s.cachedAt = rawCopy(next), s.options.Now()
	return next
}
func (s *Store) plaintextSourceJSON() json.RawMessage {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	return sourceJSON(data)
}
func rawTruthy(value json.RawMessage) bool {
	if value == nil {
		return false
	}
	switch string(value) {
	case "false", "0", `""`, "null":
		return false
	}
	return true
}
func (s *Store) sourceFallback(primary json.RawMessage) json.RawMessage {
	if s.options.Platform != "darwin" {
		return s.plaintextSourceJSON()
	}
	if primary != nil {
		return rawCopy(primary)
	}
	secondary := s.plaintextSourceJSON()
	if !rawTruthy(secondary) {
		return json.RawMessage("{}")
	}
	return secondary
}

func (s *Store) ReadSourceJSON(ctx context.Context) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var primary json.RawMessage
	if s.options.Platform == "darwin" {
		primary = s.primarySourceJSON(ctx)
	}
	return s.sourceFallback(primary), ctx.Err()
}

// ReadSourceJSONAsync shares the exact keychain cache/generation with sync reads
// and updates. Concurrent uncached async reads join one physical subprocess;
// an update or invalidation prevents the old result from replacing new data.
func (s *Store) ReadSourceJSONAsync(ctx context.Context) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.options.Platform != "darwin" {
		return s.plaintextSourceJSON(), ctx.Err()
	}
	s.mu.Lock()
	if s.cacheFresh() {
		primary := rawCopy(s.cachedJSON)
		s.mu.Unlock()
		return s.sourceFallback(primary), ctx.Err()
	}
	if flight := s.readInFlight; flight != nil {
		s.mu.Unlock()
		select {
		case <-flight.done:
			return s.sourceFallback(flight.data), ctx.Err()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	previous := rawCopy(s.cachedJSON)
	generation := s.generation
	flight := &sourceReadFlight{done: make(chan struct{})}
	s.readInFlight = flight
	s.mu.Unlock()
	output, err := s.options.RunSecurity(ctx, Command{Args: []string{"find-generic-password", "-a", s.account, "-w", "-s", s.service}})
	var next json.RawMessage
	if err == nil && len(output) > 0 {
		next = sourceJSON([]byte(protocol.TrimJavaScriptString(string(protocol.DecodeJavaScriptUTF8(output)))))
	}
	s.mu.Lock()
	if generation == s.generation {
		if next == nil {
			next = previous
		}
		s.cachedJSON, s.cachedAt = rawCopy(next), s.options.Now()
		s.readInFlight = nil
	}
	flight.data = rawCopy(next)
	close(flight.done)
	s.mu.Unlock()
	return s.sourceFallback(next), ctx.Err()
}

// UpdateSourceJSON is the same writer/cache, accepting the original JSON root
// after a CLI property mutation instead of silently imposing a record schema.
func (s *Store) UpdateSourceJSON(ctx context.Context, raw json.RawMessage) Result {
	data, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return Result{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return Result{}
	}
	return s.updateSourceJSON(ctx, data)
}
