// Package voice owns the original local dictation service and native worker.
package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const ProviderID = "sensevoice-local"
const MaxAudioBytes = 4 * 1024 * 1024
const MaxAudioSeconds = 120

var Languages = []string{"auto", "zh", "en", "ja", "ko", "yue"}

func Language(value string) bool {
	for _, v := range Languages {
		if v == value {
			return true
		}
	}
	return false
}
func DownloadSource(value string) bool {
	return value == "auto" || value == "official" || value == "mirror"
}

type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string { return e.Message }
func failure(code, message string) error {
	status := 500
	switch code {
	case "voice/invalid-audio":
		status = 400
	case "voice/unknown-provider":
		status = 404
	case "voice/not-ready":
		status = 409
	case "BAD_REQUEST":
		status = 400
	case "NOT_FOUND":
		status = 404
	case "METHOD_NOT_ALLOWED":
		status = 405
	}
	return &Error{status, code, message}
}

type Failure struct {
	Reason   string `json:"reason"`
	Source   string `json:"source,omitempty"`
	Status   *int   `json:"status,omitempty"`
	Resource string `json:"resource,omitempty"`
	Message  string `json:"message"`
}
type Preparation struct {
	Phase            string   `json:"phase"`
	Step             string   `json:"step,omitempty"`
	Resource         string   `json:"resource,omitempty"`
	CompletedBytes   *int64   `json:"completedBytes,omitempty"`
	TotalBytes       *int64   `json:"totalBytes,omitempty"`
	ResumedFromBytes *int64   `json:"resumedFromBytes,omitempty"`
	Source           string   `json:"source,omitempty"`
	Error            *Failure `json:"error,omitempty"`
}
type Info struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Location      string   `json:"location"`
	Languages     []string `json:"languages"`
	DownloadBytes *int64   `json:"downloadBytes,omitempty"`
}
type Status struct {
	Info        Info        `json:"info"`
	Preparation Preparation `json:"preparation"`
}
type Transcript struct {
	Text             string  `json:"text"`
	AudioSeconds     float64 `json:"audioSeconds"`
	InferenceSeconds float64 `json:"inferenceSeconds"`
}
type Provider interface {
	Info() Info
	Status(context.Context) (Preparation, error)
	Prepare(context.Context, string, func(Preparation)) error
	Remove(context.Context) error
	Transcribe(context.Context, []byte, string) (Transcript, error)
	Close(context.Context) error
}
type Options struct {
	Providers         []Provider
	ReadPreferences   func(context.Context) (protocol.JSONObject, error)
	UpdatePreferences func(context.Context, protocol.JSONObject) (protocol.JSONObject, error)
}

func object(data json.RawMessage) protocol.JSONObject {
	var o protocol.JSONObject
	_ = o.UnmarshalJSON(data)
	return o
}
func str(data json.RawMessage) string {
	var text string
	if json.Unmarshal(data, &text) == nil {
		return text
	}
	return fmt.Sprint(string(data))
}
func jsString(data json.RawMessage) string {
	if data == nil {
		return "undefined"
	}
	value, e := protocol.JavaScriptStringJSON(data)
	if e != nil {
		return string(data)
	}
	return str(value)
}
func ptr(value int64) *int64 { return &value }
