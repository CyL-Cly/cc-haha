package clibootstrap

import (
	"context"
	"encoding/base64"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

func (m *MainPrint) nativeNormalizeShellImage(ctx context.Context, scope *cooperative.Scope, data []byte, extension string) (*ownerasync.OwnerFuture[string], error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	library := m.options.Native.ImageLibraryPath
	return nativeSkillWork(m, scope, func() (string, error) {
		if err := context.Cause(ctx); err != nil {
			return "", err
		}
		codec, _ := imageprocessing.BundledCodec(library)
		resized, err := imageprocessing.Normalize(codec, data, len(data), extension)
		if err != nil {
			return "", err
		}
		return "data:image/" + resized.MediaType + ";base64," + base64.StdEncoding.EncodeToString(resized.Buffer), context.Cause(ctx)
	})
}

func (m *NativeMCPToolContext) nativeMCPNormalizeShellImage(ctx context.Context, scope *cooperative.Scope, data []byte, extension string) (*ownerasync.OwnerFuture[string], error) {
	if scope == nil || !scope.BelongsTo(m.command.Owner) {
		return nil, cooperative.ErrRestricted
	}
	library := m.command.Native.ImageLibraryPath
	return nativeOwnedWork(m.command.Owner, m.binding, scope, func() (string, error) {
		if err := context.Cause(ctx); err != nil {
			return "", err
		}
		codec, _ := imageprocessing.BundledCodec(library)
		resized, err := imageprocessing.Normalize(codec, data, len(data), extension)
		if err != nil {
			return "", err
		}
		return "data:image/" + resized.MediaType + ";base64," + base64.StdEncoding.EncodeToString(resized.Buffer), context.Cause(ctx)
	})
}
