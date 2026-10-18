package clibootstrap

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"os"
	"path/filepath"
)

func (m *MainPrint) TerminalMemoryFiles(ctx context.Context) ([]protocol.JSONObject, error) {
	var rows []protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		m.userContext.RefreshMemoryFiles()
		future, err := nativeSkillWork(m, scope, m.userContext.MemoryFilesForInspection)
		if err != nil {
			return err
		}
		files, err := future.Await(scope)
		if err != nil {
			return err
		}
		for _, candidate := range []usercontext.MemoryFile{{Path: filepath.Join(m.options.Native.ConfigHomeDirectory(), "CLAUDE.md"), Type: "User"}, {Path: filepath.Join(m.options.Native.CWD, "CLAUDE.md"), Type: "Project"}} {
			found := false
			for _, file := range files {
				if file.Path == candidate.Path {
					found = true
					break
				}
			}
			if !found {
				files = append(files, candidate)
			}
		}
		rows = []protocol.JSONObject{}
		for _, file := range files {
			if file.Type == "AutoMem" || file.Type == "TeamMem" {
				continue
			}
			content, err := m.options.Native.readFile(file.Path)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			rows = append(rows, protocol.NewJSONObject(protocol.JSONField{Name: "path", Value: quoteMain(file.Path)}, protocol.JSONField{Name: "type", Value: quoteMain(file.Type)}, protocol.JSONField{Name: "content", Value: quoteMain(string(content))}))
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return rows, err
}
func (m *MainPrint) WriteTerminalMemory(ctx context.Context, path, text string) error {
	files, err := m.TerminalMemoryFiles(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, file := range files {
		if file.String("path") == path {
			found = true
			break
		}
	}
	if !found {
		return errors.New("Memory file is no longer available")
	}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Input, func(scope *cooperative.Scope) error {
		if err := m.options.Native.check("memory editor write", path); err != nil {
			return err
		}
		future, err := nativeSkillWork(m, scope, func() (struct{}, error) {
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, os.WriteFile(path, []byte(text), 0600)
		})
		if err != nil {
			return err
		}
		if _, err = future.Await(scope); err != nil {
			return err
		}
		m.userContext.RefreshMemoryFiles()
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
