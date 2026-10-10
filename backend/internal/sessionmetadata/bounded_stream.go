package sessionmetadata

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// BoundedProfile selects one consumer's retained structural envelope. The
// tokenizer and every original metadata default stay shared and unchanged.
type BoundedProfile struct {
	Selected                    func([]string) bool
	Preview                     func([]string) bool
	MetadataBytes, PreviewChars int
	LimitError                  func() error
}
type BoundedStream struct {
	parser                      tokenizer
	projection                  projection
	parseError, projectionError error
	sawContent, closed          bool
}

func NewBoundedStream(profile BoundedProfile) (*BoundedStream, error) {
	if profile.Selected == nil || profile.Preview == nil || profile.MetadataBytes <= 0 || profile.PreviewChars < 0 || profile.LimitError == nil {
		return nil, errors.New("Bounded JSON stream requires its explicit consumer profile")
	}
	return &BoundedStream{projection: projection{profile: &profile}}, nil
}
func (p *projection) selected(path []string) bool {
	if p.profile == nil {
		return selected(path)
	}
	for _, part := range path {
		if part == unselected {
			return false
		}
	}
	return p.profile.Selected(path)
}
func (p *projection) preview(path []string) bool {
	if p.profile == nil {
		return preview(path)
	}
	return p.profile.Preview(path)
}
func (p *projection) budget() int {
	if p.profile == nil {
		return metadataBytes
	}
	return p.profile.MetadataBytes
}
func (p *projection) previewBudget() int {
	if p.profile == nil {
		return previewChars
	}
	return p.profile.PreviewChars
}
func (p *projection) limit() error {
	if p.profile == nil {
		return metadataLimit()
	}
	return p.profile.LimitError()
}
func (s *BoundedStream) feed(part []byte, final bool) {
	if s.parseError != nil {
		return
	}
	tokens, err := s.parser.feed(part, final)
	if err != nil {
		s.parseError = err
		return
	}
	for _, token := range tokens {
		if s.projectionError != nil {
			continue
		}
		if err = s.projection.token(token); err != nil {
			s.projectionError = err
		}
	}
}
func (s *BoundedStream) Write(part []byte) error {
	if s.closed {
		return errors.New("Bounded JSON stream is closed")
	}
	if !s.sawContent {
		for _, b := range part {
			if b != ' ' && b != '\t' && b != '\n' && b != '\r' && b != '\v' && b != '\f' {
				s.sawContent = true
				break
			}
		}
	}
	s.feed(part, false)
	return nil
}

// Finish deliberately gives syntax failures precedence over a budget failure,
// matching the original oversized-record consumer's malformed-line accounting.
func (s *BoundedStream) Finish() (json.RawMessage, error) {
	if s.closed {
		return nil, errors.New("Bounded JSON stream is closed")
	}
	s.closed = true
	if !s.sawContent {
		return json.RawMessage{}, nil
	}
	s.feed(nil, true)
	if s.parseError != nil {
		return json.RawMessage("{"), nil
	}
	if s.projectionError != nil {
		return nil, s.projectionError
	}
	if s.projection.root == nil {
		return json.RawMessage{}, nil
	}
	var result bytes.Buffer
	s.projection.root.write(&result)
	return protocol.ParseAndStringifyJavaScriptJSON(result.Bytes())
}
func (s *BoundedStream) Discard() {
	s.closed = true
	s.projection.frames = nil
	s.projection.root = nil
	s.projection.scalar = nil
	s.parser.tokens = nil
	s.parser.stack = nil
}
