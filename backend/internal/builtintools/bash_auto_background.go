package builtintools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

// Source compares the entire first deprecated-split part with "sleep". Keep
// that literal behavior, including arguments, instead of correcting its intent.
func nativeBashAutoBackgroundAllowed(command, platform string) (bool, error) {
	classifier, e := sessionrewind.NewRecordedCommandClassifier(platform, "")
	if e != nil {
		return false, e
	}
	parts, e := classifier.SplitForExitCode(command)
	if e != nil {
		return false, e
	}
	if len(parts) == 0 {
		return true, nil
	}
	return protocol.TrimJavaScriptString(parts[0]) != "sleep", nil
}
