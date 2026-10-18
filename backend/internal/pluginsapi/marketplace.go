package pluginsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func ReadKnownMarketplaces(ctx context.Context, pluginsDir string) (protocol.JSONObject, error) {
	if err := ctx.Err(); err != nil {
		return protocol.JSONObject{}, err
	}
	data, err := os.ReadFile(filepath.Join(pluginsDir, "known_marketplaces.json"))
	return ParseKnownMarketplaces(data, err)
}

// ParseKnownMarketplaces is the original loadKnownMarketplacesConfig body after
// its actual file await. It performs no I/O, cache mutation or path sampling.
func ParseKnownMarketplaces(data []byte, readFailure error) (protocol.JSONObject, error) {
	if os.IsNotExist(readFailure) {
		return rec(), nil
	}
	if readFailure != nil {
		return protocol.JSONObject{}, fmt.Errorf("Failed to load marketplace configuration: %s", readFailure)
	}
	data = protocol.DecodeJavaScriptUTF8(data)
	if !json.Valid(data) {
		message, _ := provider.PinnedJavaScriptJSONSyntaxMessage(string(data))
		return protocol.JSONObject{}, fmt.Errorf("Failed to load marketplace configuration: %s", stringValue(message))
	}
	schemas, err := pluginschema.Declarations()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	validated, issues := pluginschema.Validate(schemas.Known, data)
	if len(issues) > 0 {
		messages := []string{}
		for _, issue := range issues {
			parts := []string{}
			for _, p := range issue.Path {
				parts = append(parts, fmt.Sprint(p))
			}
			messages = append(messages, strings.Join(parts, ".")+": "+issue.Message)
		}
		return protocol.JSONObject{}, fmt.Errorf("Marketplace configuration file is corrupted: %s", strings.Join(messages, ", "))
	}
	return obj(validated), nil
}
func ReadMarketplace(location string) (protocol.JSONObject, error) {
	schemas, err := pluginschema.Declarations()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	data, err := lspRead(filepath.Join(location, ".claude-plugin", "marketplace.json"))
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), syscall.ENOTDIR.Error()) {
			data, err = lspRead(location)
		}
	}
	if err != nil {
		return protocol.JSONObject{}, err
	}
	validated, issues := pluginschema.Validate(schemas.Marketplace, data)
	if len(issues) > 0 {
		return protocol.JSONObject{}, fmt.Errorf("Invalid marketplace: %s", pluginschema.ErrorJSON(issues, nil))
	}
	return obj(validated), nil
}

// LookupCachedPlugin is the cache-first primitive. A caller must bind the
// actual marketplace fetch fallback separately; nil does not mean fetched.
func LookupCachedPlugin(ctx context.Context, pluginsDir, id string) (*MarketplacePlugin, error) {
	name, market := splitID(id)
	if name == "" || market == "" {
		return nil, nil
	}
	known, err := ReadKnownMarketplaces(ctx, pluginsDir)
	if err != nil {
		return nil, nil
	}
	entry := obj(known.Get(market))
	if !entry.IsObject() || entry.String("installLocation") == "" {
		return nil, nil
	}
	catalog, err := ReadMarketplace(entry.String("installLocation"))
	if err != nil {
		return nil, nil
	}
	for _, v := range arr(catalog.Get("plugins")) {
		e := obj(v)
		if e.String("name") == name {
			return &MarketplacePlugin{e, entry.String("installLocation")}, nil
		}
	}
	return nil, nil
}
