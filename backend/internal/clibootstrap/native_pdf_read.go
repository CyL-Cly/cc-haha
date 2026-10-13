package clibootstrap

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/pdfread"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func newNativePDFReader(n *NativeBoundary, directory string) (*pdfread.Reader, error) {
	codec, _ := imageprocessing.BundledCodec(n.ImageLibraryPath)
	return pdfread.New(pdfread.Options{Files: pdfread.NativeFiles{Check: n.check}, Run: pdfread.NativeRunner(n.Environment, n.CWD), UUID: uuidMain, ResultsDirectory: directory, Codec: codec})
}
func pdfRawFuture(owner *cooperative.Owner, scope *cooperative.Scope, work *ownerasync.OwnerFuture[pdfread.Result], cache *builtinwrite.FileStateCache, path string) (*ownerasync.OwnerFuture[json.RawMessage], error) {
	output, err := ownerasync.NewOwnerFuture[json.RawMessage](owner)
	if err != nil {
		return nil, err
	}
	_, err = work.Then(scope, func(next *cooperative.Scope, value pdfread.Result, failure error) error {
		if failure != nil {
			return output.Reject(next, failure)
		}
		if value.CacheFull {
			offset := float64(1)
			if err := cache.Set(next, path, builtinwrite.FileState{Content: quoteMain(""), Timestamp: value.Timestamp, Offset: &offset}); err != nil {
				return output.Reject(next, err)
			}
		}
		return output.Resolve(next, value.Data)
	})
	return output, err
}
func (m *MainPrint) startNativePDFRead(ctx context.Context, scope *cooperative.Scope, path string, pages *string) (*ownerasync.OwnerFuture[json.RawMessage], error) {
	if !scope.BelongsTo(m.Owner) || m.Host == nil || m.nativeFileCache == nil {
		return nil, cooperative.ErrRestricted
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	lifetime := m.nativePDFLife.context()
	actual, err := m.Host.SnapshotContextInspection(scope)
	if err != nil {
		return nil, err
	}
	directory, err := m.Controller.ToolResultsDirectoryOnOwner(scope)
	if err != nil {
		return nil, err
	}
	if m.nativePDFReader == nil {
		m.nativePDFReader, err = newNativePDFReader(m.options.Native, directory)
		if err != nil {
			return nil, err
		}
	}
	environment, err := m.nativeProcessEnvironment(scope)
	if err != nil {
		return nil, err
	}
	reader := m.nativePDFReader.WithResultsDirectory(directory).WithRunner(pdfread.NativeRunner(environment, m.options.Native.CWD))
	work, err := nativeSkillWork(m, scope, func() (pdfread.Result, error) { return reader.Read(lifetime, path, pages, actual.MainModel) })
	if err != nil {
		return nil, err
	}
	return pdfRawFuture(m.Owner, scope, work, m.nativeFileCache, path)
}
func (m *MainPrint) nativePDFMetadataMessage(scope *cooperative.Scope, data json.RawMessage) (*queryloop.Message, error) {
	if !scope.BelongsTo(m.Owner) || m.nativePDFReader == nil {
		return nil, cooperative.ErrRestricted
	}
	reader := m.nativePDFReader
	work, err := nativeSkillWork(m, scope, func() (json.RawMessage, error) { return reader.Metadata(m.nativePDFLife.context(), data) })
	if err != nil {
		return nil, err
	}
	content, err := work.Await(scope)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 || string(content) == "[]" {
		return nil, nil
	}
	record := messages.OrderedUser(m.factory.CreateUser(storage.Object{"content": content, "isMeta": json.RawMessage("true")}))
	return &queryloop.Message{Record: record}, nil
}
func (m *NativeMCPToolContext) nativeMCPPDFDirectory() string {
	config := m.command.Native.Environment["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = filepath.Join(m.command.Native.Home, ".claude")
	}
	cwd, sessionID := m.command.Native.CWD, m.nativePDFSessionID
	if m.state != nil {
		snapshot := m.state.Snapshot()
		cwd, sessionID = snapshot.OriginalCWD, snapshot.SessionID
	}
	if sessionID == "" {
		sessionID = uuidMain()
		m.nativePDFSessionID = sessionID
	}
	return filepath.Join(config, "projects", projectpath.Sanitize(cwd), sessionID, "tool-results")
}
func (m *NativeMCPToolContext) startNativeMCPPDFRead(ctx context.Context, scope *cooperative.Scope, path string, pages *string) (*ownerasync.OwnerFuture[json.RawMessage], error) {
	if !scope.BelongsTo(m.command.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	lifetime := m.nativePDFLife.context()
	directory := m.nativeMCPPDFDirectory()
	var err error
	if m.nativePDFReader == nil {
		m.nativePDFReader, err = newNativePDFReader(m.command.Native, directory)
		if err != nil {
			return nil, err
		}
	}
	reader := m.nativePDFReader.WithResultsDirectory(directory).WithRunner(pdfread.NativeRunner(m.command.Native.Environment, m.command.Native.CWD))
	model := m.model
	work, err := nativeOwnedWork(m.command.Owner, m.binding, scope, func() (pdfread.Result, error) {
		result, err := reader.Read(lifetime, path, pages, model)
		if err == nil {
			// Source MCP still executes original call's page image normalization,
			// while its facade intentionally exposes only call.data.
			_, err = reader.Metadata(lifetime, result.Data)
		}
		return result, err
	})
	if err != nil {
		return nil, err
	}
	return pdfRawFuture(m.command.Owner, scope, work, m.cache, path)
}
func isPDFReadData(data json.RawMessage) bool {
	var value protocol.JSONObject
	if json.Unmarshal(data, &value) != nil {
		return false
	}
	return value.String("type") == "pdf" || value.String("type") == "parts"
}
