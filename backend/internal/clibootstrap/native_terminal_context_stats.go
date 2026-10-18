package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"path/filepath"
	"time"
)

// TerminalEffort uses the same current-session inference configuration and
// user-settings producer as the original local command, on its owning actor.
func (m *MainPrint) TerminalEffort(ctx context.Context, args string) (string, error) {
	var result string
	err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if m.closed || m.Host == nil {
			return cooperative.ErrClosed
		}
		var err error
		result, err = m.nativeEffortCommand(scope, args)
		return err
	})
	return result, err
}

// TerminalActivityStats reads the original physical transcript/cache consumer.
// Paths come from the already authorized native process, never ambient HOME.
func (m *MainPrint) TerminalActivityStats(ctx context.Context, dateRange string) (protocol.JSONObject, error) {
	var result protocol.JSONObject
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.Host == nil {
			return cooperative.ErrClosed
		}
		n := m.options.Native
		config := n.ConfigHomeDirectory()
		if config == "" {
			config = filepath.Join(n.Home, ".claude")
		}
		if !filepath.IsAbs(config) {
			config = filepath.Join(n.CWD, config)
		}
		future, err := nativeSkillWork(m, scope, func() (protocol.JSONObject, error) {
			location := time.Local
			if zone := n.Environment["TZ"]; zone != "" {
				if parsed, err := time.LoadLocation(zone); err == nil {
					location = parsed
				}
			}
			service, err := activitystatsapi.New(activitystatsapi.Options{HomeDir: n.Home, ConfigDir: config, Now: n.Now, Location: location})
			if err != nil {
				return protocol.JSONObject{}, err
			}
			stats, err := service.Aggregate(ctx, dateRange, n.Now())
			if err != nil {
				return protocol.JSONObject{}, err
			}
			data, err := stats.MarshalJSON()
			var value protocol.JSONObject
			if err == nil {
				err = value.UnmarshalJSON(data)
			}
			return value, err
		})
		if err != nil {
			return err
		}
		result, err = future.Await(scope)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}

func (m *MainPrint) TerminalStatsModelLabels(ctx context.Context, stats protocol.JSONObject) (map[string]string, error) {
	labels := map[string]string{}
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.Host == nil {
			return cooperative.ErrClosed
		}
		usage, _ := stats.Object("modelUsage")
		for _, property := range usage.OwnProperties() {
			var name string
			if json.Unmarshal(property.NameJSON, &name) != nil {
				continue
			}
			label, err := m.nativeModelSelector.PublicModelDisplayName(quoteMain(name))
			if err != nil {
				return err
			}
			if label != "" {
				labels[name] = label
			}
		}
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return labels, err
}
