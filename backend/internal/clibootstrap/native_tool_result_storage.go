package clibootstrap

import (
	"context"
	"errors"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolresultstorage"
)

func (m *MainPrint) nativeToolResult(_ context.Context, scope *cooperative.Scope, block protocol.JSONObject, name string, declared float64) (protocol.JSONObject, error) {
	if !scope.BelongsTo(m.Owner) || m.Controller == nil || m.options.Native == nil {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	threshold := toolresultstorage.Threshold(name, declared, m.nativeAnalyticsConfig(scope, "tengu_satin_quoll"))
	processed, err := toolresultstorage.Process(block, name, threshold, toolresultstorage.Files{})
	if !errors.Is(err, toolresultstorage.ErrRequiresPersistence) {
		return processed, err
	}
	directory, err := m.Controller.ToolResultsDirectoryOnOwner(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	n := m.options.Native
	// Retain only the original immutable session path in the IO worker; all
	// model/history state and delivery stay on the original owner.
	future, err := nativeSkillWork(m, scope, func() (protocol.JSONObject, error) {
		return toolresultstorage.Process(block, name, threshold, toolresultstorage.Files{Directory: directory,
			MkdirAll: func(path string) error {
				if err := n.check("mkdir", path); err != nil {
					return err
				}
				return os.MkdirAll(path, 0777)
			},
			WriteExclusive: func(path string, data []byte) error {
				if err := n.check("writeFile", path); err != nil {
					return err
				}
				file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
				if errors.Is(err, os.ErrExist) {
					return nil
				}
				if err != nil {
					return err
				}
				_, err = file.Write(data)
				closeError := file.Close()
				if err != nil {
					return err
				}
				return closeError
			},
		})
	})
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return future.Await(scope)
}
