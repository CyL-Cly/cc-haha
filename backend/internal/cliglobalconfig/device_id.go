package cliglobalconfig

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// GetOrCreateUserID is the original per-request producer. Truthy existing
// values are retained as JSON; only falsy/missing values create 32-byte hex.
func (service *Service) GetOrCreateUserID() (json.RawMessage, error) {
	config, err := service.Get()
	if err != nil {
		return nil, err
	}
	if value := config.Get("userID"); jsTruthy(value) {
		return value, nil
	}
	entropy := make([]byte, 32)
	if _, err = rand.Read(entropy); err != nil {
		return nil, err
	}
	value, _ := json.Marshal(hex.EncodeToString(entropy))
	err = service.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		next := current.Clone()
		next.Set("userID", value)
		return &next, nil
	})
	return value, err
}
