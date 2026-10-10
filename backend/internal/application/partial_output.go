package application

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

// Keep completed blocks from the last failed response, preserving Source's
// response identity, turn reset and nested-agent exclusions.
type printPartialOutputTracker struct {
	responseID                   string
	responseText                 string
	responseIsKnownError         bool
	previousResponseText         string
	previousResponseIsKnownError bool
}

func (t *printPartialOutputTracker) observe(message protocol.JSONObject) {
	kind := message.String("type")
	root := string(message.Get("parent_tool_use_id")) == "null"
	if kind == "system" && message.String("subtype") == "compact_boundary" || kind == "user" && root {
		*t = printPartialOutputTracker{}
		return
	}
	if kind != "assistant" || !root {
		return
	}
	response := obj(message.Get("message"))
	var text strings.Builder
	for _, block := range array(response.Get("content")) {
		if block.String("type") == "text" {
			text.WriteString(block.String("text"))
		}
	}
	if t.responseID != response.String("id") {
		t.previousResponseText, t.previousResponseIsKnownError = t.responseText, t.responseIsKnownError
		t.responseID, t.responseText, t.responseIsKnownError = response.String("id"), "", false
	}
	t.responseText += text.String()
	t.responseIsKnownError = t.responseIsKnownError || message.Get("error") != nil || strings.HasPrefix(text.String(), "API Error:")
}

func (t *printPartialOutputTracker) formatResult(result string, isError bool) string {
	if isError && t.responseText == result && !t.previousResponseIsKnownError && t.previousResponseText != "" && t.previousResponseText != result {
		return t.previousResponseText + "\n" + result
	}
	return result
}

func (t *printPartialOutputTracker) formatResultLine(result string, isError bool) string {
	text := t.formatResult(result, isError)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text
}
