package analytics

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
)

// RetryPrevious is the original exporter's constructor background operation.
// It selects only this session's files and never changes another session's
// telemetry or the current process file.
func (s *Service) RetryPrevious(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	if !s.loggerEnabled || s.closed {
		return nil
	}
	completion, e := ownerasync.NewOwnerFuture[struct{}](s.o.Owner)
	if e != nil {
		return e
	}
	s.pending = append(s.pending, completion)
	directory := filepath.Join(s.o.ConfigDir, "telemetry")
	prefix := "1p_failed_events." + s.o.SessionID(scope) + "."
	ref, e := s.o.Binding.Acquire(scope)
	if e != nil {
		return e
	}
	go func() {
		names, _ := s.o.Files.ReadDirNames(directory)
		receipt, e := s.o.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			defer ref.DeliveryFinished()
			defer completion.Resolve(next, struct{}{})
			if s.closed {
				return nil
			}
			for _, name := range names {
				if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".json") && !strings.Contains(name, s.processID) {
					if e := s.retryOldFile(next, filepath.Join(directory, name)); e != nil {
						return e
					}
				}
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

func (s *Service) retryOldFile(scope *cooperative.Scope, path string) error {
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
		raw, _ := s.o.Files.ReadFile(path)
		receipt, e := s.o.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			defer ref.DeliveryFinished()
			events := decodeJSONLines(raw)
			if s.closed {
				return completion.Resolve(next, struct{}{})
			}
			if s.attempts >= s.maxAttempts(next) || len(events) == 0 {
				return s.saveOld(next, path, nil, completion)
			}
			return s.sendOld(next, path, events, 0, completion)
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

func (s *Service) sendOld(scope *cooperative.Scope, path string, events []json.RawMessage, index int, completion *ownerasync.OwnerFuture[struct{}]) error {
	if index >= len(events) {
		return s.saveOld(scope, path, nil, completion)
	}
	config := s.o.Config(scope, "tengu_1p_event_batch_config")
	size := positive(config, "maxExportBatchSize", 200)
	if size <= 0 {
		size = 200
	}
	end := min(index+size, len(events))
	if string(s.o.Config(scope, "tengu_frond_boric").Get("firstParty")) == "true" {
		return s.saveOld(scope, path, events[index:], completion)
	}
	body, e := json.Marshal(map[string]any{"events": events[index:end]})
	if e != nil {
		return s.saveOld(scope, path, events[index:], completion)
	}
	request, e := s.o.Prepare(scope, s.endpoint(scope), body, string(config.Get("skipAuth")) == "true")
	if e != nil {
		return s.saveOld(scope, path, events[index:], completion)
	}
	return s.http(scope, request, func(next *cooperative.Scope, response Response, failure error) error {
		if failure == nil && response.Status >= 200 && response.Status < 300 {
			return s.sendOldNext(next, path, events, end, completion)
		}
		if failure == nil && response.Status == 401 && hasAuth(request.Headers) {
			request.Headers = withoutAuth(request.Headers)
			return s.http(next, request, func(last *cooperative.Scope, r Response, failure error) error {
				if failure == nil && r.Status >= 200 && r.Status < 300 {
					return s.sendOldNext(last, path, events, end, completion)
				}
				return s.saveOld(last, path, events[index:], completion)
			})
		}
		return s.saveOld(next, path, events[index:], completion)
	})
}

func (s *Service) sendOldNext(scope *cooperative.Scope, path string, events []json.RawMessage, index int, completion *ownerasync.OwnerFuture[struct{}]) error {
	if index >= len(events) {
		return s.saveOld(scope, path, nil, completion)
	}
	_, e := scope.ArmTimer(100*time.Millisecond, func() {
		_, _ = s.o.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error { return s.sendOld(next, path, events, index, completion) })
	})
	return e
}

func (s *Service) saveOld(scope *cooperative.Scope, path string, events []json.RawMessage, completion *ownerasync.OwnerFuture[struct{}]) error {
	raw := []byte{}
	for _, event := range events {
		raw = append(raw, event...)
		raw = append(raw, '\n')
	}
	ref, e := s.o.Binding.Acquire(scope)
	if e != nil {
		return e
	}
	go func() {
		if len(events) == 0 {
			_ = s.o.Files.Remove(path)
		} else {
			_ = s.o.Files.Write(path, raw)
		}
		receipt, e := s.o.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
			defer ref.DeliveryFinished()
			return completion.Resolve(next, struct{}{})
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
