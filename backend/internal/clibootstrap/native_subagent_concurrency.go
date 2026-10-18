package clibootstrap

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"path/filepath"
	"sync"
)

// A domain is one CLI runtime incarnation, not the process hosting all desktop
// sessions. Detached children inherit its pointer; independent roots/process
// teammates receive a new domain. Unlimited runs still hold a counted lease.
type SubagentConcurrency struct {
	mu     sync.Mutex
	active int64
}

func (d *SubagentConcurrency) reserve(limit *int64) (func(), error) {
	return d.reserveCurrent(func() *int64 { return limit })
}

func (d *SubagentConcurrency) reserveCurrent(currentLimit func() *int64) (func(), error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	limit := currentLimit()
	if limit != nil && d.active >= *limit {
		return nil, fmt.Errorf("Maximum concurrent subagents reached (%d). Wait for a running subagent to finish before spawning another. The user can change this limit in Settings > General.", *limit)
	}
	d.active++
	var once sync.Once
	return func() { once.Do(func() { d.mu.Lock(); defer d.mu.Unlock(); d.active-- }) }, nil
}
func (m *MainPrint) reserveOrdinarySubagent() (func(), error) {
	config := m.options.Native.ConfigHomeDirectory()
	if config == "" {
		config = filepath.Join(m.options.Native.Home, ".claude")
	}
	return m.options.OrdinarySubagents.reserveCurrent(func() *int64 {
		data, err := m.options.Native.readFile(filepath.Join(config, "settings.json"))
		if err != nil {
			return nil
		}
		var value map[string]json.RawMessage
		if json.Unmarshal(data, &value) != nil {
			return nil
		}
		if limit, valid := settings.PositiveSafeInteger(value["maxConcurrentSubagents"]); valid {
			return &limit
		}
		return nil
	})
}
