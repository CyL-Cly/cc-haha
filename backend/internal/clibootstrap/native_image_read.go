package clibootstrap

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type nativeImageRead struct {
	image        imageprocessing.Result
	originalSize int
}

func (m *MainPrint) startNativeImageRead(ctx context.Context, scope *cooperative.Scope, path string, tokens float64) (*ownerasync.OwnerFuture[json.RawMessage], error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	library := m.options.Native.ImageLibraryPath
	work, err := nativeSkillWork(m, scope, func() (nativeImageRead, error) {
		if err := context.Cause(ctx); err != nil {
			return nativeImageRead{}, err
		}
		data, err := m.options.Native.readFile(path)
		if err != nil {
			return nativeImageRead{}, err
		}
		codec, _ := imageprocessing.BundledCodec(library)
		output, err := imageprocessing.ReadImage(codec, data, path, tokens)
		if err == nil {
			err = context.Cause(ctx)
		}
		return nativeImageRead{output, len(data)}, err
	})
	if err != nil {
		return nil, err
	}
	future, err := ownerasync.NewOwnerFuture[json.RawMessage](m.Owner)
	if err != nil {
		return nil, err
	}
	_, err = work.Then(scope, func(next *cooperative.Scope, value nativeImageRead, failure error) error {
		if failure != nil {
			return future.Reject(next, failure)
		}
		file := protocol.NewJSONObject(protocol.JSONField{Name: "base64", Value: quoteMain(base64.StdEncoding.EncodeToString(value.image.Buffer))}, protocol.JSONField{Name: "type", Value: quoteMain("image/" + value.image.MediaType)}, protocol.JSONField{Name: "originalSize", Value: rawMain(value.originalSize)})
		if value.image.Dimensions != nil {
			file.Set("dimensions", rawMain(value.image.Dimensions))
		}
		return future.Resolve(next, rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("image")}, protocol.JSONField{Name: "file", Value: rawMain(file)})))
	})
	return future, err
}
func (m *MainPrint) nativeImageMetadataMessage(scope *cooperative.Scope, data json.RawMessage) (*queryloop.Message, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	var result protocol.JSONObject
	if err := result.UnmarshalJSON(data); err != nil {
		return nil, err
	}
	if result.String("type") != "image" {
		return nil, nil
	}
	file, _ := result.Object("file")
	var dimensions imageprocessing.Dimensions
	if json.Unmarshal(file.Get("dimensions"), &dimensions) != nil {
		return nil, nil
	}
	text := imageprocessing.MetadataText(&dimensions, "")
	if text == "" {
		return nil, nil
	}
	record := messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": quoteMain(text), "isMeta": json.RawMessage("true")}))
	return &queryloop.Message{Record: record}, nil
}
