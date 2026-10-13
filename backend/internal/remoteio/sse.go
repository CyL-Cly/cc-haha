package remoteio

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) sseLoop() {
	var reconnectStart time.Time
	attempt := 0
	for s.ctx.Err() == nil {
		u, _ := url.Parse(s.endpoints.Stream)
		s.mu.Lock()
		sequence := s.sequence
		s.mu.Unlock()
		if sequence > 0 {
			query := u.Query()
			query.Set("from_sequence_num", strconv.Itoa(sequence))
			u.RawQuery = query.Encode()
		}
		ctx, cancel := context.WithCancel(s.ctx)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			cancel()
			s.finish(0, err)
			return
		}
		req.Header = s.headers(true)
		req.Header.Set("Accept", "text/event-stream")
		if sequence > 0 {
			req.Header.Set("Last-Event-ID", strconv.Itoa(sequence))
		}
		response, err := s.doGlobalHTTP(req)
		if err == nil {
			if PermanentHTTP(response.StatusCode) {
				_ = response.Body.Close()
				cancel()
				s.finish(response.StatusCode, nil)
				return
			}
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				reconnectStart = time.Time{}
				attempt = 0
				timer := time.AfterFunc(45*time.Second, cancel)
				buffer := make([]byte, 8192)
				remaining := ""
				for {
					n, failure := response.Body.Read(buffer)
					if n > 0 {
						frames, rest, parseErr := ParseSSEFrames(remaining + string(buffer[:n]))
						if parseErr != nil {
							timer.Stop()
							_ = response.Body.Close()
							cancel()
							s.diagnostic("cli_sse_frame_too_large")
							// Source treats a rejected oversized stream as terminal
							// input closure, without a model/SDK execution error.
							s.finish(0, nil)
							return
						}
						remaining = rest
						for _, frame := range frames {
							timer.Reset(45 * time.Second)
							s.sseFrame(frame)
						}
					}
					if failure != nil {
						break
					}
				}
				timer.Stop()
			} else {
				_, _ = io.Copy(io.Discard, response.Body)
			}
			_ = response.Body.Close()
		}
		cancel()
		if s.ctx.Err() != nil {
			return
		}
		now := s.options.Now()
		if reconnectStart.IsZero() {
			reconnectStart = now
		}
		if now.Sub(reconnectStart) >= 10*time.Minute {
			s.finish(0, nil)
			return
		}
		attempt++
		s.diagnostic("cli_sse_reconnect_attempt")
		if s.options.Wait(s.ctx, s.retryDelay(attempt, 30*time.Second, 0.25)) != nil {
			return
		}
	}
}

func (s *Service) sseFrame(frame SSEFrame) {
	if number, ok := parseInteger(frame.ID); ok {
		s.mu.Lock()
		duplicate := s.seen[number]
		if !duplicate {
			s.seen[number] = true
		}
		if number > s.sequence {
			s.sequence = number
		}
		if len(s.seen) > 1000 {
			for n := range s.seen {
				if n < s.sequence-200 {
					delete(s.seen, n)
				}
			}
		}
		s.mu.Unlock()
		if duplicate {
			s.diagnostic("cli_sse_duplicate_sequence")
		}
	}
	if frame.Event == "" || frame.Data == "" {
		return
	}
	if frame.Event != "client_event" {
		s.diagnostic("cli_sse_unexpected_event_type")
		return
	}
	var event protocol.JSONObject
	if event.UnmarshalJSON([]byte(frame.Data)) != nil {
		return
	}
	// JavaScript's synchronous onEvent callback runs before its input iterator
	// continuation. Preserve that received-before-processing order across Go
	// goroutines by admitting the ACK before publishing bytes to the reader.
	if s.ccr != nil {
		s.ccr.reportDelivery(event.String("event_id"), "received")
	}
	payload := object(event.Get("payload"))
	if payload.IsObject() && payload.Get("type") != nil {
		data, err := protocol.EncodeNDJSON(payload)
		if err == nil {
			s.receive(data)
		}
	}
}
