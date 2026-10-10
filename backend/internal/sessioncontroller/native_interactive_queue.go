package sessioncontroller

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func interactiveSlash(content json.RawMessage) bool {
	if units, e := protocol.DecodeJavaScriptJSONString(content); e == nil {
		units = trimUnits(units)
		return len(units) > 0 && units[0] == '/'
	}
	var blocks []protocol.JSONObject
	_ = json.Unmarshal(content, &blocks)
	for _, block := range blocks {
		if block.String("type") == "text" {
			units, _ := protocol.DecodeJavaScriptJSONString(block.Get("text"))
			units = trimUnits(units)
			return len(units) > 0 && units[0] == '/'
		}
	}
	return false
}
func (c *Controller) takeInteractiveBatch(scope *cooperative.Scope, selected int) ([]command, error) {
	head := c.queue[selected]
	single := head.Mode == "bash" || interactiveSlash(head.Content) || c.inboxPending(head.UUID)
	indices := []int{selected}
	if !single {
		indices = nil
		for i, item := range c.queue {
			if c.options.WriterOptions.AgentID == "" && item.AgentID != "" || item.Mode != head.Mode || item.Meta != head.Meta || interactiveSlash(item.Content) {
				continue
			}
			// Pending SDK receipts keep their original individual lifecycle fence.
			if c.inboxPending(item.UUID) {
				indices = []int{selected}
				break
			}
			indices = append(indices, i)
		}
	}
	batch := make([]command, 0, len(indices))
	for _, i := range indices {
		batch = append(batch, c.queue[i])
	}
	for i := len(indices) - 1; i >= 0; i-- {
		index := indices[i]
		c.queue = append(c.queue[:index:index], c.queue[index+1:]...)
	}
	for range batch {
		c.options.OnPhase("dequeue")
		if e := c.logQueueOwned(scope, "dequeue", nil); e != nil {
			return nil, e
		}
	}
	return batch, nil
}

type QueuedInput struct {
	Content, UUID, Origin json.RawMessage
	Mode                  string
	Meta, SkipSlash       bool
}

func queuedInputs(batch []command) []QueuedInput {
	out := make([]QueuedInput, len(batch))
	for i, item := range batch {
		out[i] = QueuedInput{Content: append(json.RawMessage(nil), item.Content...), UUID: append(json.RawMessage(nil), item.UUID...), Origin: append(json.RawMessage(nil), item.Origin...), Mode: item.Mode, Meta: item.Meta, SkipSlash: item.SkipSlash}
	}
	return out
}
