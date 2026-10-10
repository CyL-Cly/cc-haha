package messages

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"regexp"
)

var nativeBashProgressID = regexp.MustCompile(`^bash-progress-[0-9]+$`)
var nativeBashProgressTask = regexp.MustCompile(`^b[a-z0-9]{8}$`)

// This retained record is produced by the bound file-mode Bash poller. It
// enters the original mutable history/record/normalizer path; unknown progress
// variants remain behind the existing capability boundary.
func IsNativeBashProgress(record protocol.JSONObject) bool {
	data, ok := record.Object("data")
	if !ok || data.String("type") != "bash_progress" || !nativeBashProgressID.MatchString(record.String("toolUseID")) || !nativeBashProgressTask.MatchString(data.String("taskId")) {
		return false
	}
	if _, e := protocol.DecodeJavaScriptJSONString(record.Get("parentToolUseID")); e != nil {
		return false
	}
	for _, key := range []string{"output", "fullOutput"} {
		if _, e := protocol.DecodeJavaScriptJSONString(data.Get(key)); e != nil {
			return false
		}
	}
	for _, key := range []string{"elapsedTimeSeconds", "totalLines", "totalBytes"} {
		var value float64
		if e := json.Unmarshal(data.Get(key), &value); e != nil || value < 0 || math.IsInf(value, 0) || math.IsNaN(value) || math.Trunc(value) != value {
			return false
		}
	}
	expected := 7
	if timeout := data.Get("timeoutMs"); timeout != nil {
		var value float64
		if json.Unmarshal(timeout, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
		expected++
	}
	return len(data.OwnProperties()) == expected
}
