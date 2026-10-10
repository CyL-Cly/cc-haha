package sessionrewind

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"sort"
)

type turn struct {
	user        protocol.JSONObject
	activeIndex int
	messages    []protocol.JSONObject
	completed   bool
}
type indexedMessage struct {
	index int
	value protocol.JSONObject
}

func toolIDs(messages []protocol.JSONObject) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, message := range messages {
		if text(message, "type") != "tool_use" {
			continue
		}
		for _, block := range blocks(message) {
			id := text(block, "id")
			if text(block, "type") == "tool_use" && id != "" && !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
	}
	return ids
}
func buildTurns(messages []protocol.JSONObject) []turn {
	turns := []turn{}
	children := map[string][]indexedMessage{}
	for i, message := range messages {
		if text(message, "type") == "user" {
			turns = append(turns, turn{user: message, activeIndex: i})
		}
		if owner := text(message, "parentToolUseId"); owner != "" {
			children[owner] = append(children[owner], indexedMessage{i, message})
		}
	}
	for i := range turns {
		end := len(messages)
		if i+1 < len(turns) {
			end = turns[i+1].activeIndex
		}
		parent := []protocol.JSONObject{}
		for _, message := range messages[turns[i].activeIndex+1 : end] {
			if truthy(message.Get("parentToolUseId")) {
				continue
			}
			parent = append(parent, message)
			switch text(message, "type") {
			case "assistant", "tool_use", "tool_result", "error":
				turns[i].completed = true
			}
		}
		pending := toolIDs(parent)
		expanded := map[string]bool{}
		included := map[string]bool{}
		for _, message := range parent {
			included[text(message, "id")] = true
		}
		owned := []indexedMessage{}
		for cursor := 0; cursor < len(pending); cursor++ {
			id := pending[cursor]
			if expanded[id] {
				continue
			}
			expanded[id] = true
			for _, child := range children[id] {
				key := text(child.value, "id")
				if included[key] {
					continue
				}
				included[key] = true
				owned = append(owned, child)
				pending = append(pending, toolIDs([]protocol.JSONObject{child.value})...)
			}
		}
		sort.SliceStable(owned, func(a, b int) bool { return owned[a].index < owned[b].index })
		turns[i].messages = parent
		for _, child := range owned {
			turns[i].messages = append(turns[i].messages, child.value)
		}
	}
	return turns
}
