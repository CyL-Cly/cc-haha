package sessionprojection

import (
	"encoding/json"
	"testing"
)

func TestSyncTranscriptFastMetadataPreservesExplicitFalseAcrossPartialPatches(t *testing.T) {
	for _, flag := range []string{"true", "false"} {
		s, path, _ := testService(t, `{"type":"session-meta","runtimeProviderId":"openai-official","runtimeModelId":"gpt-6-sol","openAIFastMode":`+flag+`}`+"\n"+`{"type":"session-meta","permissionMode":"plan"}`+"\n", "darwin")
		value, e := s.PersistedLaunchInfo(path, "fixture", nil)
		if e != nil || value == nil || decoded(t, json.RawMessage(value))["openAIFastMode"] != (flag == "true") {
			t.Fatal(value, e)
		}
	}
}
