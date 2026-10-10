package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Request struct {
	URL     string
	Headers http.Header
	Body    []byte
	Timeout time.Duration
	Client  *http.Client // prepared immutable transport; accessed only by HTTP I/O
}
type Response struct {
	Status  int
	Headers http.Header
	Body    []byte
}
type Options struct {
	Owner       *cooperative.Owner
	Binding     *ownerasync.NativeBinding
	ConfigDir   string
	UUID        func() string
	Environment func(*cooperative.Scope) map[string]string
	Config      func(*cooperative.Scope, string) protocol.JSONObject
	SessionID   func(*cooperative.Scope) string
	Random      func() float64
	Record      func(*cooperative.Scope, Event) (protocol.JSONObject, error)
	Prepare     func(*cooperative.Scope, string, []byte, bool) (Request, error)
	HTTP        func(context.Context, Request) (Response, error)
	Files       Files
}
type Files interface {
	ReadFile(string) ([]byte, error)
	ReadDirNames(string) ([]string, error)
	Append(string, []byte) error
	Write(string, []byte) error
	Remove(string) error
}
type Service struct {
	o                              Options
	processID                      string
	retrying                       bool
	loggerEnabled, closed, working bool
	queue                          []json.RawMessage
	timer, retry, batchTimer       *cooperative.TimerTicket
	attempts                       int
	pending                        []*ownerasync.OwnerFuture[struct{}]
	ctx                            context.Context
	cancel                         context.CancelFunc
}

func New(scope *cooperative.Scope, o Options) (*Service, error) {
	if o.Owner == nil || !scope.BelongsTo(o.Owner) || !o.Binding.Owns(o.Owner) || !filepath.IsAbs(o.ConfigDir) || o.Environment == nil || o.Config == nil || o.SessionID == nil || o.Record == nil || o.Prepare == nil || o.HTTP == nil || o.UUID == nil || o.Random == nil || o.Files == nil {
		return nil, cooperative.ErrRestricted
	}
	if e := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil }); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{o: o, processID: o.UUID(), loggerEnabled: !Disabled(o.Environment(scope)), ctx: ctx, cancel: cancel}
	return s, nil
}

func (s *Service) check(scope *cooperative.Scope) error {
	if s == nil || !scope.BelongsTo(s.o.Owner) {
		return cooperative.ErrRestricted
	}
	return scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil })
}

// Log registers the enrichment reaction and immediately returns. It does not
// await HTTP, a timer, or disk persistence on the file tool's stack.
func (s *Service) Log(scope *cooperative.Scope, event Event) error {
	if e := s.check(scope); e != nil {
		return e
	}
	if s.closed || !s.loggerEnabled || Disabled(s.o.Environment(scope)) || string(s.o.Config(scope, "tengu_frond_boric").Get("firstParty")) == "true" {
		return nil
	}
	rateConfig := s.o.Config(scope, "tengu_event_sampling_config")
	var rates protocol.JSONObject
	_ = json.Unmarshal(rateConfig.Get(event.Name), &rates)
	var rate float64
	var ratePointer *float64
	if json.Unmarshal(rates.Get("sample_rate"), &rate) == nil && string(rates.Get("sample_rate")) != "null" {
		ratePointer = &rate
	}
	selected := Sample(ratePointer, s.o.Random)
	if selected != nil && *selected == 0 {
		return nil
	}
	event.Metadata = event.Metadata.Clone()
	if event.Context != nil {
		snapshot := event.Context.Clone()
		event.Context = &snapshot
	}
	if selected != nil {
		event.Metadata.Set("sample_rate", scalar(*selected))
	}
	_, e := s.o.Owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
		if s.closed {
			return nil
		}
		record, e := s.o.Record(next, event)
		if e != nil {
			return nil
		} // Original async metadata enrichment catches errors.
		config := s.o.Config(next, "tengu_1p_event_batch_config")
		if len(s.queue) >= positive(config, "maxQueueSize", 8192) {
			return nil
		}
		raw, e := record.MarshalJSON()
		if e != nil {
			return e
		}
		s.queue = append(s.queue, raw)
		if len(s.queue) >= positive(config, "maxExportBatchSize", 200) {
			return s.startFlush(next)
		}
		if s.timer == nil {
			delay := positive(config, "scheduledDelayMillis", exportInterval(s.o.Environment(next)))
			s.timer, e = next.ArmTimer(time.Duration(delay)*time.Millisecond, func() {
				_, _ = s.o.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(active *cooperative.Scope) error { s.timer = nil; return s.startFlush(active) })
			})
		}
		return e
	})
	return e
}

func exportInterval(env map[string]string) int {
	value := env["OTEL_LOGS_EXPORT_INTERVAL"]
	if value == "" {
		return 10000
	}
	value = strings.TrimSpace(value)
	n := 0
	if len(value) > 0 && (value[0] == '+' || value[0] == '-') {
		n++
	}
	for n < len(value) && value[n] >= '0' && value[n] <= '9' {
		n++
	}
	parsed, e := strconv.Atoi(value[:n])
	if e != nil {
		return 0
	}
	return parsed
}

func positive(config protocol.JSONObject, name string, fallback int) int {
	var n float64
	if json.Unmarshal(config.Get(name), &n) == nil && n != 0 {
		return int(n)
	}
	return fallback
}
func (s *Service) maxAttempts(scope *cooperative.Scope) int {
	c := s.o.Config(scope, "tengu_1p_event_batch_config")
	var n float64
	if c.Get("maxAttempts") != nil && string(c.Get("maxAttempts")) != "null" && json.Unmarshal(c.Get("maxAttempts"), &n) == nil {
		return int(n)
	}
	return 8
}

func (s *Service) endpoint(scope *cooperative.Scope) string {
	c := s.o.Config(scope, "tengu_1p_event_batch_config")
	base := c.String("baseUrl")
	if base == "" {
		base = "https://api.anthropic.com"
		if s.o.Environment(scope)["ANTHROPIC_BASE_URL"] == "https://api-staging.anthropic.com" {
			base = "https://api-staging.anthropic.com"
		}
	}
	path := c.String("path")
	if path == "" {
		path = "/api/event_logging/batch"
	}
	return base + path
}
func (s *Service) batchPath(scope *cooperative.Scope) string {
	return filepath.Join(s.o.ConfigDir, "telemetry", "1p_failed_events."+s.o.SessionID(scope)+"."+s.processID+".json")
}

func (s *Service) startFlush(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	if s.working || len(s.queue) == 0 {
		return nil
	}
	if s.timer != nil {
		_ = scope.ClearTimer(s.timer)
		s.timer = nil
	}
	if s.attempts >= s.maxAttempts(scope) {
		s.queue = nil
		return nil
	}
	s.working = true
	events := s.queue
	s.queue = nil
	completion, e := ownerasync.NewOwnerFuture[struct{}](s.o.Owner)
	if e != nil {
		s.working = false
		return e
	}
	s.pending = append(s.pending, completion)
	return s.send(scope, events, 0, completion, true)
}

// Export is the original exporter boundary for a batch already emitted by its
// log processor. It shares the same queue, attempt budget and file producer.
func (s *Service) Export(scope *cooperative.Scope, records []protocol.JSONObject) (*ownerasync.OwnerFuture[struct{}], error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if s.closed {
		return nil, cooperative.ErrClosed
	}
	events := make([]json.RawMessage, 0, len(records))
	for _, record := range records {
		raw, e := record.MarshalJSON()
		if e != nil {
			return nil, e
		}
		events = append(events, raw)
	}
	future, e := ownerasync.NewOwnerFuture[struct{}](s.o.Owner)
	if e != nil {
		return nil, e
	}
	s.pending = append(s.pending, future)
	if len(events) == 0 || s.attempts >= s.maxAttempts(scope) {
		_ = future.Resolve(scope, struct{}{})
		return future, nil
	}
	if e = s.send(scope, events, 0, future, false); e != nil {
		return nil, e
	}
	return future, nil
}

// BeginFlush starts the actual background exporter without suspending a caller
// on its completion. Only process shutdown/forceFlush await pending exports.
func (s *Service) BeginFlush(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	return s.startFlush(scope)
}

func (s *Service) send(scope *cooperative.Scope, events []json.RawMessage, index int, completion *ownerasync.OwnerFuture[struct{}], queued bool) error {
	if index >= len(events) {
		s.resetBackoff(scope)
		if queued {
			s.working = false
		}
		_ = completion.Resolve(scope, struct{}{})
		if len(s.queue) > 0 && !s.working {
			return s.startFlush(scope)
		}
		wasRetrying := s.retrying
		s.retrying = false
		if !s.closed && (wasRetrying || s.retry == nil) {
			return s.retryCurrent(scope)
		}
		return nil
	}
	config := s.o.Config(scope, "tengu_1p_event_batch_config")
	size := positive(config, "maxExportBatchSize", 200)
	if size <= 0 {
		size = 200
	}
	end := min(index+size, len(events))
	part := events[index:end]
	if string(s.o.Config(scope, "tengu_frond_boric").Get("firstParty")) == "true" {
		return s.failed(scope, events[index:], completion, errors.New("firstParty sink killswitch active"), queued)
	}
	body, e := json.Marshal(map[string]any{"events": part})
	if e != nil {
		return s.failed(scope, events[index:], completion, e, queued)
	}
	request, e := s.o.Prepare(scope, s.endpoint(scope), body, string(config.Get("skipAuth")) == "true")
	if e != nil {
		return s.failed(scope, events[index:], completion, e, queued)
	}
	return s.http(scope, request, func(next *cooperative.Scope, response Response, failure error) error {
		if failure == nil && response.Status >= 200 && response.Status < 300 {
			return s.sendNext(next, events, end, completion, queued)
		}
		if failure == nil && response.Status == 401 && hasAuth(request.Headers) {
			request.Headers = withoutAuth(request.Headers)
			return s.http(next, request, func(last *cooperative.Scope, r Response, err error) error {
				if err == nil && r.Status >= 200 && r.Status < 300 {
					return s.sendNext(last, events, end, completion, queued)
				}
				if err == nil {
					err = fmt.Errorf("Request failed with status code %d", r.Status)
				}
				return s.failed(last, events[index:], completion, err, queued)
			})
		}
		if failure == nil {
			failure = fmt.Errorf("Request failed with status code %d", response.Status)
		}
		return s.failed(next, events[index:], completion, failure, queued)
	})
}

func (s *Service) sendNext(scope *cooperative.Scope, events []json.RawMessage, index int, completion *ownerasync.OwnerFuture[struct{}], queued bool) error {
	if index >= len(events) {
		return s.send(scope, events, index, completion, queued)
	}
	var e error
	s.batchTimer, e = scope.ArmTimer(100*time.Millisecond, func() {
		_, _ = s.o.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			s.batchTimer = nil
			return s.send(next, events, index, completion, queued)
		})
	})
	return e
}

func hasAuth(headers http.Header) bool {
	for name, values := range headers {
		if strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "x-api-key") {
			for _, value := range values {
				if value != "" {
					return true
				}
			}
		}
	}
	return false
}

func withoutAuth(headers http.Header) http.Header {
	result := headers.Clone()
	for name := range result {
		if strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "x-api-key") || strings.EqualFold(name, "anthropic-beta") {
			delete(result, name)
		}
	}
	return result
}

func (s *Service) http(scope *cooperative.Scope, request Request, done func(*cooperative.Scope, Response, error) error) error {
	ref, e := s.o.Binding.Acquire(scope)
	if e != nil {
		return e
	}
	request.Body = append([]byte(nil), request.Body...)
	request.Headers = request.Headers.Clone()
	go func() {
		response, failure := s.o.HTTP(s.ctx, request)
		receipt, e := s.o.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			defer ref.DeliveryFinished()
			return done(next, response, failure)
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			ref.DeliveryFinished()
		}
	}()
	return nil
}

func (s *Service) failed(scope *cooperative.Scope, events []json.RawMessage, completion *ownerasync.OwnerFuture[struct{}], failure error, queued bool) error {
	s.attempts++
	path := s.batchPath(scope)
	body := []byte{}
	for _, event := range events {
		body = append(body, event...)
		body = append(body, '\n')
	}
	ref, e := s.o.Binding.Acquire(scope)
	if e != nil {
		return e
	}
	go func() {
		_ = s.o.Files.Append(path, body)
		receipt, e := s.o.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			defer ref.DeliveryFinished()
			if queued {
				s.working = false
			}
			s.retrying = false
			_ = completion.Resolve(next, struct{}{})
			if e := s.scheduleRetry(next); e != nil {
				return e
			}
			if len(s.queue) > 0 && !s.working {
				return s.startFlush(next)
			}
			return nil
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			ref.DeliveryFinished()
		}
	}()
	_ = failure // Original exporter errors are independent of file tools.
	return nil
}

func (s *Service) resetBackoff(scope *cooperative.Scope) {
	s.attempts = 0
	if s.retry != nil {
		_ = scope.ClearTimer(s.retry)
		s.retry = nil
	}
}

func (s *Service) scheduleRetry(scope *cooperative.Scope) error {
	if s.closed || s.retry != nil || s.retrying {
		return nil
	}
	delay := min(500*s.attempts*s.attempts, 30000)
	var e error
	s.retry, e = scope.ArmTimer(time.Duration(delay)*time.Millisecond, func() {
		_, _ = s.o.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error { s.retry = nil; return s.retryCurrent(next) })
	})
	return e
}

func (s *Service) retryCurrent(scope *cooperative.Scope) error {
	if s.closed || s.retrying {
		return nil
	}
	completion, e := ownerasync.NewOwnerFuture[struct{}](s.o.Owner)
	if e != nil {
		return e
	}
	s.pending = append(s.pending, completion)
	path := s.batchPath(scope)
	ref, e := s.o.Binding.Acquire(scope)
	if e != nil {
		return e
	}
	go func() {
		raw, _ := s.o.Files.ReadFile(path)
		receipt, e := s.o.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			defer ref.DeliveryFinished()
			defer completion.Resolve(next, struct{}{})
			if s.closed {
				return nil
			}
			events := decodeJSONLines(raw)
			if len(events) == 0 {
				return nil
			}
			if s.attempts >= s.maxAttempts(next) {
				s.resetBackoff(next)
				return s.removeSpool(next, path)
			}
			s.retrying = true
			return s.removeThen(next, path, func(current *cooperative.Scope) error {
				future, e := ownerasync.NewOwnerFuture[struct{}](s.o.Owner)
				if e != nil {
					return e
				}
				s.pending = append(s.pending, future)
				return s.send(current, events, 0, future, false)
			})
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			ref.DeliveryFinished()
			_ = completion.RejectAdministrative(e)
		}
	}()
	return nil
}

func decodeJSONLines(raw []byte) []json.RawMessage {
	rows := []json.RawMessage{}
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i < len(raw) && raw[i] != '\n' {
			continue
		}
		if event, e := protocol.ParseAndStringifyJavaScriptJSON(raw[start:i]); e == nil {
			rows = append(rows, event)
		}
		start = i + 1
	}
	return rows
}
func (s *Service) removeSpool(scope *cooperative.Scope, path string) error {
	return s.removeThen(scope, path, nil)
}
func (s *Service) removeThenFlush(scope *cooperative.Scope, path string) error {
	return s.removeThen(scope, path, func(next *cooperative.Scope) error { return s.startFlush(next) })
}
func (s *Service) removeThen(scope *cooperative.Scope, path string, done func(*cooperative.Scope) error) error {
	completion, e := ownerasync.NewOwnerFuture[struct{}](s.o.Owner)
	if e != nil {
		return e
	}
	s.pending = append(s.pending, completion)
	ref, e := s.o.Binding.Acquire(scope)
	if e != nil {
		return e
	}
	go func() {
		_ = s.o.Files.Remove(path)
		receipt, e := s.o.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			defer ref.DeliveryFinished()
			defer completion.Resolve(next, struct{}{})
			if done != nil {
				return done(next)
			}
			return nil
		})
		if e == nil {
			e = receipt.Wait()
		}
		if e != nil {
			ref.DeliveryFinished()
			_ = completion.RejectAdministrative(e)
		}
	}()
	return nil
}

func (s *Service) Flush(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	if e := scope.AwaitFulfilled(); e != nil {
		return e
	}
	if e := s.startFlush(scope); e != nil {
		return e
	}
	for i := 0; i < len(s.pending); i++ {
		if _, e := s.pending[i].Await(scope); e != nil {
			return e
		}
	}
	s.pending = nil
	return nil
}
func (s *Service) Shutdown(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	if s.closed {
		return nil
	}
	// Let already-admitted metadata reactions join the logger before shutdown.
	if e := scope.AwaitFulfilled(); e != nil {
		return e
	}
	s.closed = true
	if s.timer != nil {
		_ = scope.ClearTimer(s.timer)
		s.timer = nil
	}
	if s.retry != nil {
		_ = scope.ClearTimer(s.retry)
		s.retry = nil
	}
	// Original process exit races exporter shutdown against 500ms. Cancellation
	// closes the actual HTTP operation here rather than leaving a worker alive.
	deadline, e := scope.ArmTimer(500*time.Millisecond, s.cancel)
	if e != nil {
		return e
	}
	e = s.Flush(scope)
	_ = scope.ClearTimer(deadline)
	s.cancel()
	return e
}
