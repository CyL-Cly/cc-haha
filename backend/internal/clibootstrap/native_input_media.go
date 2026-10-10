package clibootstrap

import (
	"context"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
)

type nativeInputMediaResult struct {
	Content  json.RawMessage
	Metadata []json.RawMessage
}

func (m *MainPrint) nativeInputMedia(ctx context.Context, scope *cooperative.Scope, content json.RawMessage) (json.RawMessage, []json.RawMessage, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, nil, cooperative.ErrRestricted
	}
	input := append(json.RawMessage(nil), content...)
	library := m.options.Native.ImageLibraryPath
	future, err := nativeSkillWork(m, scope, func() (nativeInputMediaResult, error) {
		if err := context.Cause(ctx); err != nil {
			return nativeInputMediaResult{}, err
		}
		codec, _ := imageprocessing.BundledCodec(library)
		value, texts, err := imageprocessing.InputBlocks(ctx, codec, input)
		return nativeInputMediaResult{value, texts}, err
	})
	if err != nil {
		return nil, nil, err
	}
	value, err := future.Await(scope)
	return value.Content, value.Metadata, err
}
