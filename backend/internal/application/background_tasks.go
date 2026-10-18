package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

func (d *DesktopSession) stopBackgroundTask(ctx context.Context, id string) error {
	id = strings.TrimFunc(id, func(r rune) bool {
		return r >= 9 && r <= 13 || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
	if id == "" {
		d.emit(frame("background_task_stop_failed", field("taskId", id), field("message", "Background task id is required")))
		return nil
	}
	engine := d.currentEngine()
	result, e := engine.process.StopBackgroundTask(ctx, id)
	if e != nil {
		d.emit(frame("background_task_stop_failed", field("taskId", id), field("message", e.Error())))
		return nil
	}
	if string(result.Get("ok")) != "true" {
		d.emit(frame("background_task_stop_failed", field("taskId", id), protocol.JSONField{Name: "message", Value: result.Get("message")}))
		return nil
	}
	// A nil Handle error also represents a reported failed control. Only this
	// actual successful owner result may confirm the exact task's stop.
	d.mu.Lock()
	current := !d.closed && d.engine == engine
	claimed := current && d.onBackgroundTaskStopped != nil
	var persist func()
	if claimed {
		// Claim only while the actual engine identity is locked. The callback
		// performs no persistence, output, owner command or application call.
		persist = d.onBackgroundTaskStopped(id, string(result.Get("alreadyGone")) == "true")
	}
	d.mu.Unlock()
	if claimed {
		if persist != nil {
			persist()
		}
		return nil
	}
	if !current {
		return nil
	}
	if string(result.Get("alreadyGone")) == "true" {
		d.emit(frame("system_notification", field("subtype", "task_notification"), field("data", map[string]any{"type": "system", "subtype": "task_notification", "task_id": id, "status": "stopped", "reason": "not_found"})))
	}
	return nil
}

func backgroundValueTruthy(v json.RawMessage) bool {
	if len(v) == 0 || string(v) == "null" || string(v) == "false" || string(v) == `""` || string(v) == "0" {
		return false
	}
	return true
}
