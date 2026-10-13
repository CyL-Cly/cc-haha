package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// ReplaceCompactHistory retains canonical Writer identity and replaces only
// the live context, after the complete summary/hooks transaction succeeded.
func (a *SerialASK) ReplaceCompactHistory(scope *cooperative.Scope, history []queryloop.Message) error {
	if !scope.BelongsTo(a.owner) || !a.controller.options.NativeTools {
		return cooperative.ErrRestricted
	}
	records := make([]*protocol.JSONObject, 0, len(history))
	live := map[*protocol.JSONObject]*streamhistory.Envelope{}
	for _, message := range history {
		record := message.Record
		if message.Assistant != nil {
			record = message.Assistant.Record
			live[&record] = message.Assistant
		}
		if !record.IsObject() {
			return capability("compact history object")
		}
		records = append(records, &record)
	}
	a.controller.messages = records
	a.live = live
	// The ASK snapshot must also change, otherwise Finally resurrects the cache.
	a.controller.readCache = newReadCache(headlessReadCacheEntries)
	if a.running {
		a.cache = a.controller.readCache.Clone()
	}
	return nil
}

func (c *Controller) CompactInvokedSkillsOnOwner(scope *cooperative.Scope, agentID string) ([]protocol.JSONObject, error) {
	if c == nil || c.resume == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return nil, cooperative.ErrRestricted
	}
	out := []protocol.JSONObject{}
	for _, entry := range c.resume.State().Skills {
		info, _ := entry.Object("value")
		if info.String("agentId") == agentID {
			out = append(out, info)
		}
	}
	return out, nil
}

func (a *SerialASK) PersistCompactHistory(scope *cooperative.Scope) error {
	if !scope.BelongsTo(a.owner) || !a.controller.options.NativeTools || a.pending == nil {
		return cooperative.ErrRestricted
	}
	values := []LiveRecord{}
	for _, message := range a.History() {
		if message.Assistant != nil {
			values = append(values, LiveRecord{Assistant: message.Assistant})
		} else {
			record := message.Record
			values = append(values, LiveRecord{Record: &record})
		}
	}
	options, e := a.controller.recordOptions(scope)
	if e != nil {
		return e
	}
	source := a.controller.options.QuerySource
	if source == "" {
		source = "sdk"
	}
	future, e := a.pending.BeginRecordLive(scope, a.controller.options.ResumeOptions.Loader, LiveRecordProfile{QuerySource: source}, values, options)
	if e != nil {
		return e
	}
	_, e = future.Await(scope)
	return e
}

func (c *Controller) ReappendCompactMetadataOnOwner(scope *cooperative.Scope) error {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	return c.writer.RefreshMetadata()
}
