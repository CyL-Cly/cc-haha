package connectors

import (
	_ "embed"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// catalog.json is the original pinned application catalog and recipe lock,
// not captured Agent responses or a runtime substitute. Every catalog entry
// remains present even when its transport needs a separately bound capability.
//
//go:embed data/catalog.json
var catalogJSON []byte

type RemoteRecipe struct {
	ID        string `json:"id"`
	PluginID  string `json:"pluginId"`
	Version   string `json:"version"`
	Endpoint  string `json:"endpoint"`
	Transport string `json:"transport"`
	Auth      struct {
		Type     string `json:"type"`
		ClientID string `json:"clientId,omitempty"`
		In       string `json:"in,omitempty"`
		Name     string `json:"name,omitempty"`
		Prefix   string `json:"prefix,omitempty"`
	} `json:"auth"`
}

type SkillRecipe struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	License    string `json:"license"`
	Files      []struct {
		Source    string `json:"source"`
		Target    string `json:"target"`
		Integrity string `json:"integrity"`
	} `json:"files"`
}

type Catalog struct {
	Definitions []protocol.JSONObject `json:"definitions"`
	Remote      []RemoteRecipe        `json:"remote"`
	Skills      []SkillRecipe         `json:"skills"`
}

func DefaultCatalog() (Catalog, error) {
	var data Catalog
	err := json.Unmarshal(catalogJSON, &data)
	return data, err
}
