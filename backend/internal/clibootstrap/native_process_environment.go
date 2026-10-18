package clibootstrap

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
)

// The external map is populated before constructing MainPrint. Once settings
// apply, the process's existing scalar environment is the live authority;
// background plugin work must not race owner-local environment assignments.
func (n *NativeBoundary) bindProcessEnvironment(environment *clistartupenv.Environment) {
	n.mu.Lock()
	n.processEnvironment = environment
	n.mu.Unlock()
}

func (n *NativeBoundary) nativeEnvironmentSnapshot() map[string]string {
	n.mu.Lock()
	environment := n.processEnvironment
	n.mu.Unlock()
	values := map[string]string{}
	if environment == nil {
		for name, value := range n.Environment {
			values[name] = value
		}
		return values
	}
	for _, field := range environment.Snapshot().OwnProperties() {
		if field.Value == nil {
			continue
		}
		var name, value string
		_ = json.Unmarshal(field.NameJSON, &name)
		_ = json.Unmarshal(field.Value, &value)
		values[name] = value
	}
	return values
}

func (n *NativeBoundary) nativeEnvironmentValue(name string) (string, bool) {
	n.mu.Lock()
	environment := n.processEnvironment
	n.mu.Unlock()
	if environment == nil {
		value, ok := n.Environment[name]
		return value, ok
	}
	raw := environment.Get(name)
	if raw == nil {
		return "", false
	}
	var value string
	_ = json.Unmarshal(raw, &value)
	return value, true
}
