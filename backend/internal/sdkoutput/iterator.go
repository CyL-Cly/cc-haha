package sdkoutput

import "encoding/json"

// MessageIterator is the synchronous normalizeMessage producer. Construction
// leaves its body suspended. NormalizeMessages builds the block array on the
// first Next; session/parent getters and the SDK wrapper are evaluated only at
// the corresponding yield. Views retain the original selected content block.
type MessageIterator struct {
	normalizer      *Normalizer
	message         Message
	started, closed bool
	values          []*View
	index           int
	parent          func() json.RawMessage
	progress        *View
}

func (n *Normalizer) IterateMessage(message Message) *MessageIterator {
	return &MessageIterator{normalizer: n, message: message}
}

func (iterator *MessageIterator) initialize() error {
	message, n := iterator.message, iterator.normalizer
	kind := message.Record.String("type")
	iterator.parent = func() json.RawMessage { return json.RawMessage("null") }
	if kind == "assistant" || kind == "user" {
		var err error
		iterator.values, err = n.NormalizeMessages([]Message{message})
		return err
	}
	if kind != "progress" {
		return nil
	}
	var data = message.Record.Clone()
	if err := data.UnmarshalJSON(message.Record.Get("data")); err != nil || !data.IsObject() {
		return cap("original progress data object")
	}
	if data.String("type") == "agent_progress" || data.String("type") == "skill_progress" {
		var nested Message
		var err error
		if message.Nested != nil {
			nested = *message.Nested
		} else {
			nested, err = Decode(data.Get("message"))
		}
		if err != nil {
			return err
		}
		iterator.parent = func() json.RawMessage { return message.Record.Get("parentToolUseID") }
		iterator.values, err = n.NormalizeMessages([]Message{nested})
		return err
	}
	// This branch has at most one synchronous yield. Its original throttle,
	// clock and environment policy are shared with the existing normalization.
	return n.NormalizeMessage(message, func(view *View) error { iterator.progress = view; return nil })
}

func (iterator *MessageIterator) Next() (*View, bool, error) {
	if iterator.closed {
		return nil, true, nil
	}
	if !iterator.started {
		iterator.started = true
		if err := iterator.initialize(); err != nil {
			iterator.closed = true
			return nil, false, err
		}
	}
	if iterator.progress != nil {
		view := iterator.progress
		iterator.progress = nil
		return view, false, nil
	}
	for iterator.index < len(iterator.values) {
		view := iterator.values[iterator.index]
		iterator.index++
		kind := view.Record.String("type")
		if kind != "assistant" && kind != "user" {
			continue
		}
		if kind == "assistant" {
			nonempty, err := nonempty(view)
			if err != nil {
				iterator.closed = true
				return nil, false, err
			}
			if !nonempty {
				continue
			}
		}
		sdk, err := iterator.normalizer.sdkView(view, iterator.parent())
		if err != nil {
			iterator.closed = true
			return nil, false, err
		}
		return sdk, false, nil
	}
	iterator.closed = true
	return nil, true, nil
}

// Return closes a suspended sync generator without executing later yields or
// session getters. The adapter separately retains its original return value.
func (iterator *MessageIterator) Return() { iterator.closed = true }
