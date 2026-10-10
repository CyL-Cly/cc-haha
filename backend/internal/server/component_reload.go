package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const connectorReloadFailure = "Connector changed on disk, but an active task could not refresh its tools and skills. Retry the connection check before use."

// ReloadConnectorSessions visits only actual live sessions. Disk presence and
// unrelated plugin errors cannot stand in for the required active tools.
func (server *Server) ReloadConnectorSessions(ctx context.Context, requested string, required *protocol.JSONObject) error {
	server.mu.Lock()
	if server.closed {
		server.mu.Unlock()
		return context.Canceled
	}
	runtimes := make([]*runtimeSession, 0, len(server.runtimes))
	for _, current := range server.runtimes {
		runtimes = append(runtimes, current)
	}
	server.mu.Unlock()
	var work sync.WaitGroup
	var mu sync.Mutex
	failed := false
	for _, current := range runtimes {
		current.mu.Lock()
		live := current.live
		retired := current.retired
		current.mu.Unlock()
		if retired || live == nil {
			continue
		}
		work.Add(1)
		go func(live LiveSession) {
			defer work.Done()
			if err := reloadLiveComponents(ctx, live, required); err != nil {
				mu.Lock()
				failed = true
				mu.Unlock()
			}
		}(live)
	}
	work.Wait()
	if failed {
		return errors.New(connectorReloadFailure)
	}
	return context.Cause(ctx)
}

func reloadLiveComponents(ctx context.Context, live LiveSession, required *protocol.JSONObject) error {
	control, ok := live.(ComponentSession)
	if !ok {
		return errors.New("active session has no component control")
	}
	timed, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	response, err := control.ControlComponents(timed, "reload_plugins", "")
	if err != nil {
		return err
	}
	if required == nil {
		return nil
	}
	name := "office-" + required.String("id")
	var plugins, commands []protocol.JSONObject
	_ = json.Unmarshal(response.Get("plugins"), &plugins)
	_ = json.Unmarshal(response.Get("commands"), &commands)
	foundPlugin, foundSkill := false, false
	for _, plugin := range plugins {
		foundPlugin = foundPlugin || plugin.String("source") == required.String("pluginId")
	}
	for _, command := range commands {
		foundSkill = foundSkill || command.String("name") == name+":"+name
	}
	if !foundPlugin || !foundSkill {
		return errors.New("Connector plugin and skill are not available in the active task")
	}
	if required.String("transport") != "mcp" {
		return nil
	}
	serverName := "plugin:" + name + ":service"
	if hasMCPServerTools(response.Get("mcpServers"), serverName) {
		return nil
	}
	if _, err = control.ControlComponents(timed, "mcp_reconnect", serverName); err != nil {
		return err
	}
	statusCtx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	response, err = control.ControlComponents(statusCtx, "mcp_status", "")
	if err != nil {
		return err
	}
	if !hasMCPServerTools(response.Get("mcpServers"), serverName) {
		return errors.New("Connector tools are not available in the active task")
	}
	return nil
}

func hasMCPServerTools(data json.RawMessage, name string) bool {
	var servers []protocol.JSONObject
	if json.Unmarshal(data, &servers) != nil {
		return false
	}
	for _, server := range servers {
		if server.String("name") != name || server.String("status") != "connected" {
			continue
		}
		var tools []protocol.JSONObject
		_ = json.Unmarshal(server.Get("tools"), &tools)
		for _, tool := range tools {
			if tool.String("name") != "" && tool.String("name") != "authenticate" {
				return true
			}
		}
	}
	return false
}
