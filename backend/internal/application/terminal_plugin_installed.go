package application

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sourcecollation"
)

func terminalPluginFlagDate(value json.RawMessage) string {
	units, err := protocol.DecodeJavaScriptJSONString(value)
	if err != nil {
		return "Invalid Date"
	}
	stamp, valid, _ := sessionprojection.ParseDateUTC(units)
	if !valid {
		return "Invalid Date"
	}
	return time.UnixMilli(int64(stamp)).In(time.Local).Format("1/2/2006")
}

func terminalPluginUnifiedInstalled(snapshot protocol.JSONObject, clients []protocol.JSONObject) ([]protocol.JSONObject, error) {
	children := map[string][]protocol.JSONObject{}
	for _, client := range clients {
		parts := strings.Split(client.String("name"), ":")
		if len(parts) >= 3 && parts[0] == "plugin" {
			row := client.Clone()
			row.Set("displayName", raw(strings.Join(parts[2:], ":")))
			children[parts[1]] = append(children[parts[1]], row)
		}
	}
	scopes := []string{}
	groups := map[string][]protocol.JSONObject{}
	add := func(scope string, row protocol.JSONObject) {
		if _, found := groups[scope]; !found {
			scopes = append(scopes, scope)
		}
		row.Set("scope", raw(scope))
		groups[scope] = append(groups[scope], row)
	}
	loaded := array(snapshot.Get("manageInstalled"))
	if snapshot.Get("manageInstalled") == nil {
		loaded = array(snapshot.Get("installed"))
	}
	for _, value := range loaded {
		if value.Get("manifest") == nil || string(value.Get("blocked")) == "true" {
			continue
		}
		row := value.Clone()
		row.Set("itemType", raw("plugin"))
		scope := row.String("scope")
		add(scope, row)
		for _, client := range children[row.String("name")] {
			childScope := scope
			if scope == "builtin" {
				childScope = "user"
			}
			child := terminalPluginMCPRow(client)
			child.Set("name", client.Get("displayName"))
			child.Set("indented", raw(true))
			add(childScope, child)
		}
	}
	for _, client := range clients {
		name := client.String("name")
		if name == "ide" || strings.HasPrefix(name, "plugin:") {
			continue
		}
		scope := client.String("scope")
		if scope == "" {
			scope = obj(client.Get("config")).String("scope")
		}
		add(scope, terminalPluginMCPRow(client))
	}
	flags := obj(snapshot.Get("flaggedPlugins"))
	for _, failed := range terminalFailedPluginRows(snapshot) {
		if flags.Get(failed.String("id")) != nil {
			continue
		}
		row := failed.Clone()
		row.Set("itemType", raw("failed-plugin"))
		add(row.String("scope"), row)
	}
	for _, property := range flags.OwnProperties() {
		var id string
		_ = json.Unmarshal(property.NameJSON, &id)
		parts := strings.Split(id, "@")
		market := "unknown"
		if len(parts) > 1 && parts[1] != "" {
			market = parts[1]
		}
		name := parts[0]
		if name == "" {
			name = id
		}
		row := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: property.NameJSON}, protocol.JSONField{Name: "name", Value: raw(name)}, protocol.JSONField{Name: "marketplace", Value: raw(market)}, protocol.JSONField{Name: "itemType", Value: raw("flagged-plugin")}, protocol.JSONField{Name: "flaggedAt", Value: obj(property.Value).Get("flaggedAt")})
		add("flagged", row)
	}
	priority := map[string]int{"flagged": -1, "project": 0, "local": 1, "user": 2, "enterprise": 3, "managed": 4, "dynamic": 5, "builtin": 6}
	rank := func(scope string) int {
		if value, found := priority[scope]; found {
			return value
		}
		return 99
	}
	sort.SliceStable(scopes, func(i, j int) bool { return rank(scopes[i]) < rank(scopes[j]) })
	comparator, err := sourcecollation.New()
	if err != nil {
		return nil, err
	}
	defer comparator.Close()
	compare := func(a, b protocol.JSONObject) bool {
		left, _ := protocol.DecodeJavaScriptJSONString(a.Get("name"))
		right, _ := protocol.DecodeJavaScriptJSONString(b.Get("name"))
		return comparator.Compare(left, right) < 0
	}
	result := []protocol.JSONObject{}
	for _, scope := range scopes {
		rows := groups[scope]
		plugins := [][]protocol.JSONObject{}
		standalone := []protocol.JSONObject{}
		for i := 0; i < len(rows); {
			row := rows[i]
			i++
			if row.String("itemType") != "mcp" {
				group := []protocol.JSONObject{row}
				for i < len(rows) && rows[i].String("itemType") == "mcp" && string(rows[i].Get("indented")) == "true" {
					group = append(group, rows[i])
					i++
				}
				plugins = append(plugins, group)
			} else if string(row.Get("indented")) != "true" {
				standalone = append(standalone, row)
			}
		}
		sort.SliceStable(plugins, func(i, j int) bool { return compare(plugins[i][0], plugins[j][0]) })
		sort.SliceStable(standalone, func(i, j int) bool { return compare(standalone[i], standalone[j]) })
		for _, group := range plugins {
			result = append(result, group...)
		}
		result = append(result, standalone...)
	}
	return result, nil
}

func terminalPluginMCPRow(client protocol.JSONObject) protocol.JSONObject {
	return protocol.NewJSONObject(protocol.JSONField{Name: "itemType", Value: raw("mcp")}, protocol.JSONField{Name: "id", Value: raw("mcp:" + client.String("name"))}, protocol.JSONField{Name: "name", Value: client.Get("name")}, protocol.JSONField{Name: "status", Value: client.Get("status")}, protocol.JSONField{Name: "client", Value: raw(client.Clone())})
}
