package clistartupenv

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// MergePolicyLayers shares the settings customizer with the native managed
// file/drop-in resolver. It preserves array union and unknown object fields.
func MergePolicyLayers(layers ...*protocol.JSONObject) (*protocol.JSONObject, error) {
	merged := protocol.NewJSONObject()
	found := false
	for _, layer := range layers {
		if layer == nil || len(layer.OwnProperties()) == 0 {
			continue
		}
		found = true
		if err := mergeSettings(&merged, *layer); err != nil {
			return nil, err
		}
	}
	if !found {
		return nil, nil
	}
	return &merged, nil
}
