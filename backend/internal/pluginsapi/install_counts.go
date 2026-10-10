package pluginsapi

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const installCountsURL = "https://raw.githubusercontent.com/anthropics/claude-plugins-official/refs/heads/stats/stats/plugin-installs.json"

func (r *MarketplaceResolver) installCounts(ctx context.Context) map[string]json.RawMessage {
	path := filepath.Join(r.options.PluginsDir, "install-counts-cache.json")
	data, err := os.ReadFile(path)
	if err == nil {
		value := obj(data)
		date, e := time.Parse(time.RFC3339Nano, value.String("fetchedAt"))
		counts := arr(value.Get("counts"))
		valid := string(value.Get("version")) == "1" && e == nil && r.options.Now().Sub(date) <= 24*time.Hour && value.Get("counts") != nil && len(value.Get("counts")) > 0 && value.Get("counts")[0] == '['
		for _, item := range counts {
			entry := obj(item)
			var name string
			var number float64
			if json.Unmarshal(entry.Get("plugin"), &name) != nil || json.Unmarshal(entry.Get("unique_installs"), &number) != nil {
				valid = false
				break
			}
		}
		if valid {
			return countsMap(counts)
		}
	}
	timeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(timeout, "GET", installCountsURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "axios/1.14.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	response, err := r.options.Client.Do(req)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil
	}
	data, err = webfetch.ReadAxiosResponseBody(response)
	if err != nil {
		return nil
	}
	value := obj(protocol.DecodeJavaScriptUTF8(data))
	counts := arr(value.Get("plugins"))
	if value.Get("plugins") == nil || len(value.Get("plugins")) == 0 || value.Get("plugins")[0] != '[' {
		return nil
	}
	// The actual Source fetched response only requires an array. It persists its
	// entries unchanged; individual values are validated on the next cache read.
	cache := rec(field("version", 1), field("fetchedAt", r.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")), field("counts", counts))
	_ = r.write(path, cache)
	return countsMap(counts)
}
func countsMap(counts []json.RawMessage) map[string]json.RawMessage {
	result := map[string]json.RawMessage{}
	for _, item := range counts {
		entry := obj(item)
		result[entry.String("plugin")] = entry.Get("unique_installs")
	}
	return result
}
func (m *MarketplaceManager) Available(ctx context.Context, installed protocol.JSONObject) ([]protocol.JSONObject, error) {
	available := []protocol.JSONObject{}
	known, err := m.Known(ctx)
	if err != nil {
		return available, nil
	}
	counts := m.options.Resolver.installCounts(ctx)
	for _, name := range properties(known) {
		entry := obj(known.Get(name))
		catalog, err := ReadMarketplace(entry.String("installLocation"))
		if err != nil {
			catalog, err = m.options.Resolver.Fetch(ctx, obj(entry.Get("source")))
		}
		if err != nil {
			continue
		}
		for _, item := range arr(catalog.Get("plugins")) {
			plugin := obj(item)
			id := plugin.String("name") + "@" + name
			// Source isPluginInstalled requires a nonempty V2 installation array.
			if len(arr(obj(installed.Get("plugins")).Get(id))) > 0 {
				continue
			}
			row := rec(field("pluginId", id), field("name", plugin.String("name")))
			for _, key := range []string{"description"} {
				if value := plugin.Get(key); value != nil {
					row.Set(key, value)
				}
			}
			row.Set("marketplaceName", raw(name))
			for _, key := range []string{"version", "source"} {
				if value := plugin.Get(key); value != nil {
					row.Set(key, value)
				}
			}
			if count := counts[id]; count != nil {
				row.Set("installCount", count)
			}
			available = append(available, row)
		}
	}
	return available, nil
}
