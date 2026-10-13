package clibootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

// PrintLoadError represents only the public print loadInitialMessages resume
// branch. SDK/desktop callers do not opt into this output contract.
type PrintLoadError struct {
	message, sessionID string
	cause              error
}

func (e *PrintLoadError) Error() string { return e.message }
func (e *PrintLoadError) Unwrap() error { return e.cause }

var printResumeUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (m *MainPrint) activatePublicPrintResume(scope *cooperative.Scope) error {
	a := m.options.Arguments
	initialSID := m.state.SessionID()
	loadError := func(message string, cause error) error {
		return &PrintLoadError{message: message, sessionID: initialSID, cause: cause}
	}
	selector := a.Resume
	resume := resumestate.Input{Source: quoteMain(selector)}
	parsedSID := selector
	if strings.HasSuffix(strings.ToLower(selector), ".jsonl") {
		// parseSessionIdentifier allocates this before loading, independently of the
		// process SID and even when the file is absent or contains no messages.
		parsedSID = uuidMain()
		resume.JSONLPath = selector
	} else if !printResumeUUID.MatchString(selector) {
		if u, e := url.Parse(selector); e == nil && u.Scheme != "" {
			m.prepareNativeResumeIdentifier()
			parsedSID = m.nativeResumeParsedSID
			resume.Source = quoteMain(parsedSID)
		} else {
			message := "Error: --resume requires a valid session ID when used with --print. Usage: claude -p --resume <session-id>"
			if !a.ResumeWithoutValue {
				message += `. Session IDs must be in UUID format (e.g., 550e8400-e29b-41d4-a716-446655440000). Provided value "` + selector + `" is not a valid UUID`
			}
			return loadError(message, nil)
		}
	}
	validate := func(result *resumestate.Result) error {
		if result == nil || len(result.Messages) == 0 {
			if m.nativeResumeIsURL || m.remoteIO != nil && m.remoteIO.Kind() == remoteio.CCR {
				return nil
			}
			return loadError("No conversation found with session ID: "+parsedSID, nil)
		}
		// The loader already checks ResumeAt against the persisted chain before
		// filtering. A pending tool call or malformed attachment can itself be
		// removed by that filtering, so its absence here is not a load failure.
		return nil
	}
	f, e := m.Controller.BeginActivate(scope, sessioncontroller.Activation{Resume: resume, ForkSession: a.ForkSession, ResumeAt: a.ResumeAt, ValidateLoaded: validate, RemoteEmptyStartup: m.nativeResumeIsURL || m.remoteIO != nil && m.remoteIO.Kind() == remoteio.CCR})
	if e != nil {
		return e
	}
	_, e = f.Await(scope)
	if e == nil {
		return nil
	}
	var typed *PrintLoadError
	if errors.As(e, &typed) || errors.Is(e, cooperative.ErrClosed) || errors.Is(e, cooperative.ErrRestricted) {
		return e
	}
	var missing *resumestate.ResumeMessageNotFoundError
	if errors.As(e, &missing) {
		return loadError(missing.Error(), e)
	}
	// Source catch emits the current SID: a later restore failure can occur
	// after SwitchSession, while the missing/empty/at checks above precede it.
	return &PrintLoadError{message: "Failed to resume session: " + e.Error(), sessionID: m.state.SessionID(), cause: e}
}

// WritePrintLoadError returns handled=false for every unrelated startup/model/
// input failure. The public CLI uses it after initialization cleanup has joined.
func WritePrintLoadError(stdout, stderr io.Writer, format string, err error) (handled bool, writeError error) {
	var load *PrintLoadError
	if !errors.As(err, &load) {
		return false, nil
	}
	if format != "stream-json" {
		_, e := fmt.Fprintln(stderr, load.message)
		return true, e
	}
	field := func(k string, v any) protocol.JSONField {
		r, _ := json.Marshal(v)
		return protocol.JSONField{Name: k, Value: r}
	}
	result := protocol.NewJSONObject(field("type", "result"), field("subtype", "error_during_execution"), field("duration_ms", 0), field("duration_api_ms", 0), field("is_error", true), field("num_turns", 0), field("stop_reason", nil), field("session_id", load.sessionID), field("total_cost_usd", 0), field("usage", modelaccounting.ZeroUsage()), field("modelUsage", protocol.NewJSONObject()), field("permission_denials", []any{}), field("uuid", uuidMain()), field("errors", []string{load.message}))
	raw, e := result.MarshalJSON()
	if e != nil {
		return true, e
	}
	raw, e = protocol.ParseAndStringifyJavaScriptJSON(raw)
	if e != nil {
		return true, e
	}
	_, e = fmt.Fprintln(stdout, string(raw))
	return true, e
}
