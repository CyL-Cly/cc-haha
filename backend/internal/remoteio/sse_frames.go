package remoteio

import (
	"errors"
	"strings"
)

const MaxSSEFrameBytes = 1024 * 1024

var ErrSSEFrameTooLarge = errors.New("SSE frame exceeds the 1048576-byte limit")

type SSEFrame struct{ Event, ID, Data string }

// ParseSSEFrames follows the original newline delimiter and field parser,
// including comment-only frames (liveness) and multiple data lines.
func ParseSSEFrames(buffer string) ([]SSEFrame, string, error) {
	frames := []SSEFrame{}
	for {
		index := strings.Index(buffer, "\n\n")
		if index < 0 {
			if len(buffer) > MaxSSEFrameBytes {
				return nil, "", ErrSSEFrameTooLarge
			}
			return frames, buffer, nil
		}
		if index > MaxSSEFrameBytes {
			return nil, "", ErrSSEFrameTooLarge
		}
		frame, comment := SSEFrame{}, false
		for _, line := range strings.Split(buffer[:index], "\n") {
			if strings.HasPrefix(line, ":") {
				comment = true
				continue
			}
			field, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				frame.Event = value
			case "id":
				frame.ID = value
			case "data":
				if frame.Data != "" {
					frame.Data += "\n"
				}
				frame.Data += value
			}
		}
		if frame.Data != "" || comment {
			frames = append(frames, frame)
		}
		buffer = buffer[index+2:]
	}
}
