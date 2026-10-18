package clibootstrap

import (
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These two projections are the real checkpoint19 mcp-git failure. Their
// identities and sampled fields agree; independent Source enrichment reversed
// only the first two records. Mutations below must still fail comparison.
func TestMigrationWireEpochIndependentMetadataCompletionKeepsEventIdentity(t *testing.T) {
	const sourceProjection = `{"events":[{"event_name":"tengu_reset_pro_to_opus_default","model":"claude-sonnet-4-5-20250929","betas":"claude-code-20250219","additional_metadata":"eyJza2lwcGVkIjp0cnVlfQ=="},{"event_name":"tengu_migrate_mcp_approval_fields_success","model":"claude-sonnet-4-5-20250929","betas":"claude-code-20250219","additional_metadata":"eyJtaWdyYXRlZENvdW50IjozfQ=="},{"event_name":"tengu_plugin_list_command","model":"claude-sonnet-4-5-20250929","betas":"claude-code-20250219"}]}`
	const nativeProjection = `{"events":[{"event_name":"tengu_migrate_mcp_approval_fields_success","model":"claude-sonnet-4-5-20250929","betas":"claude-code-20250219","additional_metadata":"eyJtaWdyYXRlZENvdW50IjozfQ=="},{"event_name":"tengu_reset_pro_to_opus_default","model":"claude-sonnet-4-5-20250929","betas":"claude-code-20250219","additional_metadata":"eyJza2lwcGVkIjp0cnVlfQ=="},{"event_name":"tengu_plugin_list_command","model":"claude-sonnet-4-5-20250929","betas":"claude-code-20250219"}]}`
	decode := func(raw string) protocol.JSONObject {
		t.Helper()
		var value protocol.JSONObject
		if err := value.UnmarshalJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		return value
	}
	project := func(rows []map[string]json.RawMessage) protocol.JSONObject {
		t.Helper()
		wireEvents := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			wireEvents = append(wireEvents, map[string]any{"event_data": row})
		}
		raw, err := json.Marshal(map[string]any{"events": wireEvents})
		if err != nil {
			t.Fatal(err)
		}
		return migrationWireEpoch(t, raw)
	}
	source, native := decode(sourceProjection), decode(nativeProjection)
	if mustMigrationJSON(source) == mustMigrationJSON(native) {
		t.Fatal("original failure must retain the actual different completion order")
	}
	if !sameMigrationWireEpoch(source, native) || !sameMigrationWireEpoch(source, source) {
		t.Fatal("complete event identities differ only by independent metadata completion")
	}
	empty := decode(`{"events":[]}`)
	if !sameMigrationWireEpoch(empty, empty) || sameMigrationWireEpoch(source, empty) || sameMigrationWireEpoch(empty, native) {
		t.Fatal("privacy/empty and event presence contract changed")
	}
	for _, field := range []string{"event_name", "model", "betas", "additional_metadata"} {
		t.Run("wrong-"+field, func(t *testing.T) {
			var rows []map[string]json.RawMessage
			if err := json.Unmarshal(native.Get("events"), &rows); err != nil {
				t.Fatal(err)
			}
			rows[0][field] = json.RawMessage(`"incorrect-epoch-value"`)
			changed := project(rows)
			if sameMigrationWireEpoch(source, changed) {
				t.Fatal("changed event projection accepted", field)
			}
		})
	}
	for _, mutation := range []string{"drop", "duplicate", "replace-with-duplicate", "move-sampled-fields-to-other-event", "drop-betas", "null-betas"} {
		t.Run(mutation, func(t *testing.T) {
			var rows []map[string]json.RawMessage
			if err := json.Unmarshal(native.Get("events"), &rows); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "drop":
				rows = rows[1:]
			case "duplicate":
				rows = append(rows, rows[0])
			case "replace-with-duplicate":
				rows[1] = rows[0]
			case "move-sampled-fields-to-other-event":
				rows[0]["additional_metadata"], rows[1]["additional_metadata"] = rows[1]["additional_metadata"], rows[0]["additional_metadata"]
			case "drop-betas":
				delete(rows[0], "betas")
			case "null-betas":
				rows[0]["betas"] = json.RawMessage(`null`)
			}
			// Use the real projection so JSON object key order cannot be
			// the reason a changed payload is rejected.
			if sameMigrationWireEpoch(source, project(rows)) {
				t.Fatal("changed event identity/count/sampled value accepted", mutation)
			}
		})
	}
}
