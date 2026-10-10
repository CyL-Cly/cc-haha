package mcpapi

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"sort"
)

// The original API orders Object.entries by name.localeCompare. Decode raw
// JSON strings directly to UTF-16 so lone surrogates never become U+FFFD.
func sortServerNames(servers []protocol.JSONObject) error {
	comparator, err := newNameComparator()
	if err != nil {
		return err
	}
	defer comparator.Close()
	type entry struct {
		value protocol.JSONObject
		units []uint16
	}
	entries := make([]entry, len(servers))
	for i, value := range servers {
		units, err := protocol.DecodeJavaScriptJSONString(value.Get("name"))
		if err != nil {
			return err
		}
		if err = comparator.Validate(units); err != nil {
			return err
		}
		entries[i] = entry{value, units}
	}
	sort.SliceStable(entries, func(i, j int) bool { return comparator.Compare(entries[i].units, entries[j].units) < 0 })
	for i, entry := range entries {
		servers[i] = entry.value
	}
	return nil
}
