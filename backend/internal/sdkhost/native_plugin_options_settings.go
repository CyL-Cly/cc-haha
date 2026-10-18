package sdkhost

import (
	"bytes"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Options storage writes the selected plugin's non-sensitive fields into one
// actual domain. It suppresses the normal notifier while invalidating the
// settings cache. This certificate cannot admit installation or permission
// changes, or another plugin/server's options.
func (h *Host) BeginNativePluginOptionsCacheEdit(scope *cooperative.Scope, id, server string) (*NativePluginCacheEdit, error) {
	if id == "" {
		return nil, cooperative.ErrRestricted
	}
	edit, err := h.BeginNativePluginCacheEdit(scope)
	if err == nil {
		edit.optionsID = id
		edit.optionsServer = server
	}
	return edit, err
}

func sameSettingsOutsidePluginOptionsProducer(before, after *protocol.JSONObject, id, server string) bool {
	if id == "" || before == nil || after == nil || !before.IsObject() || !after.IsObject() {
		return false
	}
	path := []string{"pluginConfigs", id, "options"}
	if server != "" {
		path = []string{"pluginConfigs", id, "mcpServers", server}
	}
	// Secrets-only saves can leave settings untouched. Any actual settings
	// change must retain/create the Source producer's object-valued domain;
	// deleting its parent or replacing it with null is never an options save.
	oldBytes, _ := before.MarshalJSON()
	newBytes, _ := after.MarshalJSON()
	if !bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(oldBytes), protocol.StringifyValidatedJavaScriptJSON(newBytes)) {
		leaf := *after
		for _, part := range path {
			next, ok := leaf.Object(part)
			if !ok {
				return false
			}
			leaf = next
		}
	}
	var strip func(protocol.JSONObject, []string) protocol.JSONObject
	strip = func(value protocol.JSONObject, parts []string) protocol.JSONObject {
		copy := value.Clone()
		if len(parts) == 1 {
			copy.Delete(parts[0])
			return copy
		}
		if child, ok := copy.Object(parts[0]); ok {
			child = strip(child, parts[1:])
			if len(child.OwnProperties()) == 0 {
				copy.Delete(parts[0])
			} else {
				copy.Set(parts[0], raw(child))
			}
		}
		return copy
	}
	left, right := strip(*before, path), strip(*after, path)
	fields := map[string][]byte{}
	for _, property := range left.OwnProperties() {
		fields[string(property.NameJSON)] = protocol.StringifyValidatedJavaScriptJSON(property.Value)
	}
	if len(fields) != len(right.OwnProperties()) {
		return false
	}
	for _, property := range right.OwnProperties() {
		if value, ok := fields[string(property.NameJSON)]; !ok || !bytes.Equal(value, protocol.StringifyValidatedJavaScriptJSON(property.Value)) {
			return false
		}
	}
	return true
}
