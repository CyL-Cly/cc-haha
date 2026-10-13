package clibootstrap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func (m *NativeMCPToolContext) ordinaryImageBindings() *builtintools.ImageBindings {
	return &builtintools.ImageBindings{NormalizeShellData: m.nativeMCPNormalizeShellImage, ReadPDF: m.startNativeMCPPDFRead, Read: func(ctx context.Context, scope *cooperative.Scope, path string, tokens float64) (*ownerasync.OwnerFuture[json.RawMessage], error) {
		n := m.command.Native
		work, e := nativeOwnedWork(m.command.Owner, m.binding, scope, func() (nativeImageRead, error) {
			if e := context.Cause(ctx); e != nil {
				return nativeImageRead{}, e
			}
			data, e := n.readFile(path)
			if e != nil {
				return nativeImageRead{}, e
			}
			codec, _ := imageprocessing.BundledCodec(n.ImageLibraryPath)
			output, e := imageprocessing.ReadImage(codec, data, path, tokens)
			if e == nil {
				e = context.Cause(ctx)
			}
			return nativeImageRead{output, len(data)}, e
		})
		if e != nil {
			return nil, e
		}
		out, e := ownerasync.NewOwnerFuture[json.RawMessage](m.command.Owner)
		if e != nil {
			return nil, e
		}
		_, e = work.Then(scope, func(next *cooperative.Scope, value nativeImageRead, failure error) error {
			if failure != nil {
				return out.Reject(next, failure)
			}
			file := protocol.NewJSONObject(protocol.JSONField{Name: "base64", Value: quoteMain(base64.StdEncoding.EncodeToString(value.image.Buffer))}, protocol.JSONField{Name: "type", Value: quoteMain("image/" + value.image.MediaType)}, protocol.JSONField{Name: "originalSize", Value: rawMain(value.originalSize)})
			if value.image.Dimensions != nil {
				file.Set("dimensions", rawMain(value.image.Dimensions))
			}
			return out.Resolve(next, rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("image")}, protocol.JSONField{Name: "file", Value: rawMain(file)})))
		})
		return out, e
	}, MetadataMessage: func(scope *cooperative.Scope, _ json.RawMessage) (*queryloop.Message, error) {
		// The MCP entrypoint exposes only call.data and ignores Read.newMessages.
		return nil, m.checkOrdinary(scope)
	}}
}
