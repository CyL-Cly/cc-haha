// Package publicaccess owns the separate, paired-device listener. Local
// desktop credentials and remote device cookies remain different authorities.
package publicaccess

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const PairTTL = 5 * 60 * 1000
const SessionTTL = 30 * 24 * 60 * 60 * 1000
const CookieName = "__Host-haha-remote"

type Device struct {
	ID        string          `json:"id"`
	Name      json.RawMessage `json:"name"`
	CreatedAt float64         `json:"createdAt"`
	ExpiresAt float64         `json:"expiresAt"`
	TokenHash string          `json:"tokenHash"`
}

type Stored struct {
	Fields  protocol.JSONObject
	Devices []protocol.JSONObject
}

var tokenHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func raw(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func object(data json.RawMessage) protocol.JSONObject {
	var out protocol.JSONObject
	_ = out.UnmarshalJSON(data)
	if !out.IsObject() {
		return protocol.NewJSONObject()
	}
	return out
}
func number(data json.RawMessage) (float64, bool) {
	var value float64
	if len(data) == 0 || string(data) == "null" || json.Unmarshal(data, &value) != nil {
		return 0, false
	}
	return value, !math.IsInf(value, 0) && !math.IsNaN(value)
}
func stringValue(data json.RawMessage) bool { return len(data) > 0 && data[0] == '"' }

// MigrateStore is the original additive v0/v1 migration. Device extension
// fields survive; a legacy LAN bearer is never adopted as a device cookie.
func MigrateStore(data json.RawMessage) (Stored, error) {
	value := object(data)
	versionSource := strings.TrimSpace(string(value.Get("version")))
	version, _ := strconv.ParseFloat(versionSource, 64)
	if version > 1 {
		return Stored{}, errors.New("Unsupported remote device store version")
	}
	devices := []protocol.JSONObject{}
	var rows []json.RawMessage
	_ = json.Unmarshal(value.Get("devices"), &rows)
	for _, row := range rows {
		device := object(row)
		_, created := number(device.Get("createdAt"))
		_, expires := number(device.Get("expiresAt"))
		if stringValue(device.Get("id")) && stringValue(device.Get("name")) && created && expires && tokenHashPattern.MatchString(device.String("tokenHash")) {
			devices = append(devices, device)
		}
	}
	value.Set("version", raw(1))
	value.Set("devices", raw(devices))
	return Stored{value, devices}, nil
}

func (s Stored) MarshalJSON() ([]byte, error) {
	value := s.Fields.Clone()
	value.Set("version", raw(1))
	value.Set("devices", raw(s.Devices))
	return value.MarshalJSON()
}
func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func secret() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func readStore(path string) (Stored, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = []byte(`{}`)
		err = nil
	}
	if err != nil {
		return Stored{}, err
	}
	if !json.Valid(data) {
		return Stored{}, errors.New("Invalid remote device store")
	}
	return MigrateStore(data)
}

func persistStore(path string, stored Stored) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	suffix, err := secret()
	if err != nil {
		return err
	}
	temporary := path + "." + suffix + ".tmp"
	data, err := stored.MarshalJSON()
	if err != nil {
		return err
	}
	data, err = protocol.ParseAndStringifyJavaScriptJSON(data)
	if err != nil {
		return err
	}
	if err = os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
