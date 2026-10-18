package sdkhost

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type inputImageMetadataKey struct{}
type inputImageMetadata struct {
	IDs   []int
	Texts []json.RawMessage
}

// WithInputImageMetadata carries the terminal paste producer's display IDs
// and source paths through the same owned input processor as structured SDK
// media. It is scoped to one submission, never shared with a later turn.
func WithInputImageMetadata(ctx context.Context, ids []int, texts []json.RawMessage) context.Context {
	value := inputImageMetadata{IDs: append([]int(nil), ids...)}
	for _, text := range texts {
		value.Texts = append(value.Texts, append(json.RawMessage(nil), text...))
	}
	return context.WithValue(ctx, inputImageMetadataKey{}, value)
}

func (h *Host) terminalInputImageMetadata() (inputImageMetadata, bool) {
	if h.inputModelContext == nil {
		return inputImageMetadata{}, false
	}
	value, present := h.inputModelContext.Value(inputImageMetadataKey{}).(inputImageMetadata)
	return value, present
}

// Source normalizes array images before slash dispatch, then appends image
// metadata after the real command result. Slash messages intentionally have
// no imagePasteIds; those display IDs belong only to processTextPrompt.
func (h *Host) processQueuedPrintInputBase(scope *cooperative.Scope, content, uuid json.RawMessage, skipSlash, meta bool, origin json.RawMessage, skipAttachments bool) (*sessioncontroller.ProcessedInput, error) {
	_, _, slash, parseErr := slashParts(content)
	if parseErr != nil || !slash || skipSlash || h.options.Print == nil || !h.options.Print.NativeProcess {
		return h.processQueuedPrintInputBaseNormalized(scope, content, uuid, skipSlash, meta, origin, skipAttachments)
	}
	var metadata []json.RawMessage
	if pasted, present := h.terminalInputImageMetadata(); present {
		metadata = pasted.Texts
	} else if inputContainsBase64Image(content) {
		if h.options.Print.NativeInputMedia == nil {
			return nil, cap("actual SDK input media normalization")
		}
		if h.inputModelContext == nil {
			return nil, cap("actual SDK input media lifetime")
		}
		var err error
		content, metadata, err = h.options.Print.NativeInputMedia(h.inputModelContext, scope, content)
		if err != nil {
			return nil, err
		}
	}
	result, err := h.processQueuedPrintInputBaseNormalized(scope, content, uuid, skipSlash, meta, origin, skipAttachments)
	if err != nil || result == nil || len(metadata) == 0 {
		return result, err
	}
	blocks := make([]protocol.JSONObject, len(metadata))
	for i, text := range metadata {
		blocks[i] = protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("text")}, protocol.JSONField{Name: "text", Value: text})
	}
	result.Messages = append(result.Messages, messages.OrderedUser(h.options.Factory.CreateUser(storage.Object{"content": raw(blocks), "isMeta": raw(true)})))
	return result, nil
}
