package preflightcore

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Only an actual rough-count slice renders attachments. A usable usage anchor
// must not allocate UUIDs or throw on an attachment outside that slice.
type AttachmentOptions struct {
	Factory *messages.Factory
	Plan    *attachments.PlanOptions
}

func EstimateContextWithAttachments(original []protocol.JSONObject, options *AttachmentOptions) (float64, error) {
	history, err := originalHistory(original)
	if err != nil {
		return 0, err
	}
	if err := validateUsageReads(history); err != nil {
		return 0, err
	}
	return tokenCountWithAttachments(history, options)
}
func RoughHistoryWithAttachments(original []protocol.JSONObject, options *AttachmentOptions) (float64, error) {
	history, err := originalHistory(original)
	if err != nil {
		return 0, err
	}
	return roughHistoryWithAttachments(history, options)
}
