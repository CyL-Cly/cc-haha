package tui

import (
	"encoding/json"
	"fmt"
	"runtime"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These are REPL state and refs. They do not have the mounted animation
// child's lifetime, and a ref-only delta must not commit the parent.
type terminalSpinnerStreamState struct {
	observed bool
	text     []uint16
	tools    map[int]bool
}

func (s *State) commitTerminalSpinnerParent() {
	s.ensureTerminalSpinnerClock().parentRevision++
}

func (s *State) commitTerminalSpinnerEvent(event Event) {
	clock := s.ensureTerminalSpinnerClock()
	if event.Kind == TerminalSpinnerProgressChanged {
		return
	}
	if clock.stream != nil && clock.stream.observed && (event.Kind == TextDelta || event.Kind == ThinkingDelta) {
		return
	}
	s.commitTerminalSpinnerParent()
}

func (s *State) applyTerminalSpinnerQuery(raw json.RawMessage, oldMode string) bool {
	clock := s.ensureTerminalSpinnerClock()
	if len(raw) == 0 {
		return oldMode != clock.mode
	}
	if clock.stream == nil {
		clock.stream = &terminalSpinnerStreamState{tools: map[int]bool{}}
	}
	stream := clock.stream
	stream.observed = true
	envelope := presentationObject(raw)
	kind := envelope.String("type")
	commit := oldMode != clock.mode
	allowed := !s.Preferences.ReducedMotion && runtime.GOOS != "windows"
	clearText := func() {
		if allowed && stream.text != nil {
			stream.text = nil
			commit = true
		}
		s.taskListStreaming = false
	}
	if kind != "stream_event" && kind != "stream_request_start" {
		if kind == "tool_use_summary" {
			return false
		}
		clearText()
		return true
	}
	if kind == "stream_request_start" {
		return commit
	}
	event := presentationObject(envelope.Get("event"))
	index := 0
	_ = json.Unmarshal(event.Get("index"), &index)
	switch event.String("type") {
	case "message_stop":
		stream.tools = map[int]bool{}
		commit = true
	case "content_block_start":
		clearText()
		if presentationObject(event.Get("content_block")).String("type") == "tool_use" {
			stream.tools[index] = true
			commit = true
		}
	case "content_block_delta":
		delta := presentationObject(event.Get("delta"))
		switch delta.String("type") {
		case "input_json_delta":
			commit = stream.tools[index]
		case "text_delta":
			if allowed {
				units, err := protocol.DecodeJavaScriptJSONString(delta.Get("text"))
				if err == nil {
					commit = stream.text == nil || len(units) > 0
					if stream.text == nil {
						stream.text = make([]uint16, 0)
					}
					stream.text = append(stream.text, units...)
					s.taskListStreaming = false
					for _, unit := range stream.text {
						if unit == '\n' {
							s.taskListStreaming = true
						}
					}
				}
			}
		}
	}
	return commit
}

func (s *State) advanceTerminalSpinnerAnimation(msg terminalSpinnerAnimationMsg) {
	clock := s.ensureTerminalSpinnerClock()
	s.dreamClock = msg.now
	clock.animationTime = msg.clockTime
	if !s.terminalSpinnerMounted() || s.Preferences.ReducedMotion || clock.render == nil {
		return
	}
	r := clock.render
	if msg.clockTime-r.lastAnimationUpdate < 50*time.Millisecond {
		return
	}
	r.lastAnimationUpdate = msg.clockTime
	r.animation.render(clock.responseUnits, msg.clockTime, false, s.terminalSpinnerActiveTools())
	r.childRevision++
}

func (s *State) terminalSpinnerActiveTools() bool {
	for _, entry := range s.Entries {
		if entry.TurnID == s.ActiveTurn && entry.Kind == "tool" && entry.Running {
			return true
		}
	}
	return false
}

// Thinking status owns real timeout state; elapsed rows own a one-second
// refresh. Neither refresh advances the animation token counter.
func (s *State) tickTerminalSpinnerTimers(now time.Time) {
	clock := s.ensureTerminalSpinnerClock()
	r := clock.render
	if r == nil {
		return
	}
	beforeThinking, beforeThought := r.thinking, r.thoughtFor
	r.observe(clock.mode, now)
	if beforeThinking != r.thinking || beforeThought != r.thoughtFor {
		s.commitTerminalSpinnerParent()
		return
	}
	if now.Sub(r.lastElapsedUpdate) >= time.Second {
		r.lastElapsedUpdate = now
		r.elapsedSnapshot = clock.elapsed(now)
		r.childRevision++
	}
}

func (s *State) terminalSpinnerContextKey() string {
	if s.Teammates == nil {
		return ""
	}
	t := s.Teammates
	return fmt.Sprintf("%s/%d/%s/%s/%t", t.ExpandedView, t.SelectedIPAgentIndex, t.ViewSelectionMode, t.ViewingAgentTaskID, s.ShowTeammatePreview)
}
