package pluginmcp

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"testing"
)

func TestFilesManifestPrecedenceSchemaDefaultsAndUnresolvedCacheAgainstSource(t *testing.T) {
	settings := `{"future":{"keep":true}}`
	credentials := `{"futureSecure":true}`
	s, home, env := newFixture(t, settings, credentials)
	root := filepath.Join(home, "plugin")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	env["FRESH"] = "first"
	env["EMPTY"] = ""
	plugin := parse(t, `{"name":"sample","source":"sample@mkt","repository":"sample@mkt","enabled":true,"manifest":{}}`)
	plugin.Set("path", raw(root))
	disabled := plugin.Clone()
	disabled.Set("enabled", raw(false))
	disabled.Set("name", raw("disabled"))
	plugin.Set("manifest", raw(parse(t, `{"mcpServers":["extra.json",{"same":{"type":"http","url":"https://inline.example/${FRESH}","headers":{"Auth":"${TOKEN:-fallback:-ignored}"}},"literal":{"type":"sdk","name":"${UNEXPANDED}"}},"missing.json"]}`)))
	files := map[string]string{filepath.Join(root, ".mcp.json"): `{"mcpServers":{"same":{"command":"default","extra":"strip"},"local":{"command":"${CLAUDE_PLUGIN_ROOT}/bin/${FRESH}","env":{"MISSING":"${UNKNOWN}","DUP":"${UNKNOWN}","EMPTY":"${EMPTY:-default}","DEFAULT":"${ABSENT:-one:-two}"}},"invalid":{"type":"http","url":42},"empty-command":{"command":""},"ws":{"type":"ws","url":"ws://owned.example/${FRESH}","oauth":{"clientId":"strip"}},"ide":{"type":"ws-ide","url":"${IGNORE}","ideName":"editor","authToken":"${TOKEN}","extra":"strip"},"proxy":{"type":"claudeai-proxy","url":"${IGNORE}","id":"id"}}}`, filepath.Join(root, "extra.json"): `{"same":{"command":"path"},"http":{"type":"http","url":"https://owned.example/${FRESH}","oauth":{"clientId":"fixed","callbackPort":3118,"xaa":false,"unknown":true},"headersHelper":"preserve-helper","unknown":true}}`}
	plugins := []protocol.JSONObject{plugin, disabled}
	steps := []map[string]any{{"action": "load"}, {"action": "extract"}, {"action": "env", "key": "FRESH", "value": "second", "apply": func() { env["FRESH"] = "second" }}, {"action": "get"}, {"action": "get", "plugin": 1}, {"action": "files", "files": map[string]string{filepath.Join(root, ".mcp.json"): `{"fresh-file":{"command":"new-file"}}`}}, {"action": "get"}, {"action": "extract"}, {"action": "get"}}
	compareSource(t, s, home, plugins, settings, credentials, files, map[string]string{"FRESH": "first", "EMPTY": ""}, steps)
}
func TestTopOptionsChannelSecretsMissingKeyAndChannelValidationAgainstSource(t *testing.T) {
	settings := `{"future":true,"pluginConfigs":{"sample@mkt":{"options":{"collision":"top","top":"top-value","count":1,"tags":["a","b"]},"mcpServers":{"chat":{"collision":"channel","token":"legacy-plaintext","count":3},"ready":{"token":"ok"}}}}}`
	credentials := `{"futureSecure":true,"pluginSecrets":{"sample@mkt":{"top":"top-secret"},"sample@mkt/chat":{"token":"channel-secret"},"other@mkt":{"keep":"owned"}}}`
	s, home, _ := newFixture(t, settings, credentials)
	root := filepath.Join(home, "plugin")
	_ = os.Mkdir(root, 0700)
	plugin := parse(t, `{"name":"sample","source":"sample@mkt","repository":"sample@mkt","enabled":true,"manifest":{"userConfig":{"top":{"type":"string"}},"channels":[{"server":"chat","displayName":"Chat","userConfig":{"token":{"type":"string","required":true,"sensitive":true},"count":{"type":"number","min":4}}},{"server":"ready","userConfig":{"token":{"type":"string","required":true}}},{"server":"missing","userConfig":{"must":{"type":"boolean","required":true}}},{"server":"plain"}],"mcpServers":{"chat":{"command":"run","args":["${user_config.collision}","${user_config.top}","${user_config.tags}","${user_config.count}"],"env":{"TOKEN":"${user_config.token}"}},"bad":{"type":"http","url":"https://owned.example/${user_config.missing}"},"plain":{"type":"http","url":"https://owned.example/${user_config.top}"}}}}`)
	plugin.Set("path", raw(root))
	steps := []map[string]any{{"action": "channels"}, {"action": "load-user", "id": "sample@mkt", "server": "chat"}, {"action": "get"}, {"action": "save-user", "id": "sample@mkt", "server": "chat", "values": parse(t, `{"token":"new-secret","count":5}`), "schema": parse(t, `{"token":{"sensitive":true}}`)}, {"action": "load-user", "id": "sample@mkt", "server": "chat"}, {"action": "channels"}, {"action": "save-user", "id": "sample@mkt", "server": "chat", "values": parse(t, `{"token":23,"count":"sensitive count","tags":["one","two"]}`), "schema": parse(t, `{"count":{"sensitive":true}}`)}, {"action": "load-user", "id": "sample@mkt", "server": "chat"}, {"action": "get"}, {"action": "load-user", "id": "missing", "server": "absent"}}
	compareSource(t, s, home, []protocol.JSONObject{plugin}, settings, credentials, map[string]string{}, map[string]string{}, steps)
}
func TestNoDeclaredOptionsLeavesUserPlaceholdersLiteralAndDataIsLazyForRemoteAgainstSource(t *testing.T) {
	settings := `{}`
	credentials := `{}`
	s, home, _ := newFixture(t, settings, credentials)
	root := filepath.Join(home, "plugin")
	_ = os.Mkdir(root, 0700)
	plugin := parse(t, `{"name":"remote","source":"remote@mkt","repository":"remote@mkt","enabled":true,"manifest":{"mcpServers":{"raw":{"type":"http","url":"https://owned.example/${user_config.TOKEN}"},"normal":{"type":"sse","url":"https://owned.example/${CLAUDE_PLUGIN_ROOT}"}}}}`)
	plugin.Set("path", raw(root))
	steps := []map[string]any{{"action": "load"}, {"action": "get"}, {"action": "resolve", "server": "lazy", "config": parse(t, `{"type":"http","url":"https://owned.example/${CLAUDE_PLUGIN_ROOT}"}`)}, {"action": "resolve", "server": "data", "config": parse(t, `{"type":"http","url":"https://owned.example/${CLAUDE_PLUGIN_DATA}"}`)}, {"action": "scope", "servers": parse(t, `{"server":{"command":"run","scope":"user","pluginSource":"old","unknown":"keep"}}`)}}
	compareSource(t, s, home, []protocol.JSONObject{plugin}, settings, credentials, map[string]string{}, map[string]string{}, steps)
}
func TestJSONFileEnvelopeArrayBOMInvalidAndParentPathsAgainstSource(t *testing.T) {
	settings := `{}`
	credentials := `{}`
	s, home, _ := newFixture(t, settings, credentials)
	root := filepath.Join(home, "plugin")
	_ = os.Mkdir(root, 0700)
	plugin := parse(t, `{"name":"formats","source":"formats@mkt","repository":"formats@mkt","enabled":true,"manifest":{"mcpServers":["array.json","bom.json","invalid.json","../parent.json","empty.json"]}}`)
	plugin.Set("path", raw(root))
	files := map[string]string{filepath.Join(root, "array.json"): `[{"command":"index-zero"},{"type":"sdk","name":"sdk-index"}]`, filepath.Join(root, "bom.json"): "\ufeff{\"bom\":{\"command\":\"reject-bom\"}}", filepath.Join(root, "invalid.json"): `{"bad":`, filepath.Join(home, "parent.json"): `{"parent":{"command":"parent-allowed"}}`, filepath.Join(root, "empty.json"): `null`}
	compareSource(t, s, home, []protocol.JSONObject{plugin}, settings, credentials, files, map[string]string{}, []map[string]any{{"action": "load"}, {"action": "get"}})
}

func TestAllFileTransportSchemaBranchesAndPrototypeNamesAgainstSource(t *testing.T) {
	settings := `{}`
	credentials := `{}`
	s, home, _ := newFixture(t, settings, credentials)
	root := filepath.Join(home, "plugin")
	_ = os.Mkdir(root, 0700)
	plugin := parse(t, `{"name":"schema","source":"schema@mkt","repository":"schema@mkt","enabled":true,"manifest":{"mcpServers":"schema.json"}}`)
	plugin.Set("path", raw(root))
	files := map[string]string{filepath.Join(root, "schema.json"): `{"__proto__":{"command":"prototype-only"},"stdio":{"type":"stdio","command":"run","args":["one","two"],"env":{"K":"value"}},"sse":{"type":"sse","url":"https://owned.example/","headers":{"K":"value"},"headersHelper":"helper"},"sse-ide":{"type":"sse-ide","url":"https://owned.example/","ideName":"editor","ideRunningInWindows":false},"ws-ide":{"type":"ws-ide","url":"ws://owned.example/","ideName":"editor","authToken":"token","ideRunningInWindows":true},"sdk":{"type":"sdk","name":"sdk"},"proxy":{"type":"claudeai-proxy","url":"https://owned.example/","id":"id"},"null":null,"string":"bad","empty-type":{"type":"","command":"bad"},"number-type":{"type":1,"command":"bad"},"args-not-list":{"command":"bad","args":"bad"},"args-not-strings":{"command":"bad","args":[1]},"env-not-object":{"command":"bad","env":[]},"env-not-strings":{"command":"bad","env":{"K":false}},"headers-not-object":{"type":"http","url":"x","headers":false},"headers-not-strings":{"type":"http","url":"x","headers":{"K":1}},"bad-ide":{"type":"sse-ide","url":"x","ideName":1},"bad-ide-flag":{"type":"ws-ide","url":"x","ideName":"ok","ideRunningInWindows":1},"bad-helper":{"type":"http","url":"x","headersHelper":1},"bad-oauth":{"type":"http","url":"x","oauth":false},"bad-client":{"type":"http","url":"x","oauth":{"clientId":1}},"bad-callback":{"type":"http","url":"x","oauth":{"callbackPort":1.5}},"bad-callback-range":{"type":"http","url":"x","oauth":{"callbackPort":18014398509481984}},"bad-metadata":{"type":"http","url":"x","oauth":{"authServerMetadataUrl":"http://owned.example/"}},"invalid-metadata":{"type":"http","url":"x","oauth":{"authServerMetadataUrl":"https://"}},"bad-xaa":{"type":"http","url":"x","oauth":{"xaa":1}},"future-type":{"type":"future","url":"x"}}`}
	compareSource(t, s, home, []protocol.JSONObject{plugin}, settings, credentials, files, map[string]string{}, []map[string]any{{"action": "load"}, {"action": "get"}, {"action": "extract"}})
}
