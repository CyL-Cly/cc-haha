package voice

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"sync"
)

type prepareTask struct {
	ctx    context.Context
	cancel context.CancelFunc
	state  Preparation
	done   chan struct{}
}
type Service struct {
	o         Options
	mu        sync.Mutex
	providers map[string]Provider
	tasks     map[string]*prepareTask
	outcomes  map[string]Preparation
	winding   map[string]<-chan struct{}
	closed    bool
}

func New(o Options) (*Service, error) {
	if o.ReadPreferences == nil || o.UpdatePreferences == nil {
		return nil, errors.New("voice requires the shared desktop UI preferences producer")
	}
	s := &Service{o: o, providers: map[string]Provider{}, tasks: map[string]*prepareTask{}, outcomes: map[string]Preparation{}, winding: map[string]<-chan struct{}{}}
	for _, p := range o.Providers {
		id := p.Info().ID
		if s.providers[id] != nil {
			return nil, errors.New("Voice provider already registered: " + id)
		}
		s.providers[id] = p
	}
	return s, nil
}
func (s *Service) require(id string) (Provider, error) {
	p := s.providers[id]
	if p == nil {
		return nil, failure("voice/unknown-provider", "Unknown voice provider: "+id)
	}
	return p, nil
}
func (s *Service) preparation(ctx context.Context, p Provider) (Preparation, error) {
	id := p.Info().ID
	s.mu.Lock()
	if task := s.tasks[id]; task != nil {
		state := task.state
		s.mu.Unlock()
		return state, nil
	}
	s.mu.Unlock()
	state, e := p.Status(ctx)
	if e != nil {
		return state, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if state.Phase == "ready" {
		delete(s.outcomes, id)
		return state, nil
	}
	if outcome, ok := s.outcomes[id]; ok {
		return outcome, nil
	}
	return state, nil
}
func (s *Service) Status(ctx context.Context, id string) (Status, error) {
	p, e := s.require(id)
	if e != nil {
		return Status{}, e
	}
	state, e := s.preparation(ctx, p)
	return Status{p.Info(), state}, e
}
func (s *Service) Catalog(ctx context.Context) (protocol.JSONObject, error) {
	statuses := []Status{}
	supported := false
	for _, p := range s.o.Providers {
		status, e := s.Status(ctx, p.Info().ID)
		if e != nil {
			return protocol.JSONObject{}, e
		}
		statuses = append(statuses, status)
		supported = supported || !(status.Preparation.Phase == "failed" && status.Preparation.Error != nil && status.Preparation.Error.Reason == "unsupported-platform")
	}
	preferences, e := s.o.ReadPreferences(ctx)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	return protocol.NewJSONObject(field("supported", supported), field("providers", statuses), field("preferences", preferences), field("limits", map[string]int{"maxAudioSeconds": MaxAudioSeconds, "maxAudioBytes": MaxAudioBytes})), nil
}
func field(name string, value any) protocol.JSONField {
	data, _ := json.Marshal(value)
	return protocol.JSONField{Name: name, Value: data}
}
func (s *Service) UpdatePreferences(ctx context.Context, patch protocol.JSONObject) (protocol.JSONObject, error) {
	if !patch.IsObject() {
		return protocol.JSONObject{}, failure("BAD_REQUEST", "Voice preferences must be an object")
	}
	next := protocol.NewJSONObject()
	for _, name := range []string{"enabled", "providerId", "language", "downloadSource"} {
		value := patch.Get(name)
		if value == nil {
			continue
		}
		switch name {
		case "enabled":
			if string(value) != "true" && string(value) != "false" {
				return next, failure("BAD_REQUEST", "enabled must be a boolean")
			}
		case "providerId":
			var id string
			if json.Unmarshal(value, &id) != nil || s.providers[id] == nil {
				return next, failure("voice/unknown-provider", "Unknown voice provider: "+jsString(value))
			}
		case "language":
			if !Language(str(value)) {
				return next, failure("BAD_REQUEST", "Unsupported language: "+jsString(value))
			}
		case "downloadSource":
			if !DownloadSource(str(value)) {
				return next, failure("BAD_REQUEST", "Unsupported download source: "+jsString(value))
			}
		}
		next.Set(name, value)
	}
	current, e := s.o.ReadPreferences(ctx)
	if e != nil {
		return next, e
	}
	id := next.String("providerId")
	if next.Get("providerId") == nil {
		id = current.String("providerId")
	}
	p, e := s.require(id)
	if e != nil {
		return next, e
	}
	if next.Get("language") != nil {
		if !contains(p.Info().Languages, next.String("language")) {
			return next, failure("BAD_REQUEST", p.Info().Name+" does not support language: "+next.String("language"))
		}
	} else if next.Get("providerId") != nil && !contains(p.Info().Languages, current.String("language")) {
		next.Set("language", field("", providerLanguage(p, current.String("language"))).Value)
	}
	return s.o.UpdatePreferences(ctx, next)
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func providerLanguage(p Provider, value string) string {
	languages := p.Info().Languages
	if contains(languages, value) {
		return value
	}
	if contains(languages, "auto") {
		return "auto"
	}
	if len(languages) > 0 {
		return languages[0]
	}
	return "auto"
}
func (s *Service) Prepare(ctx context.Context, id string) (Status, error) {
	p, e := s.require(id)
	if e != nil {
		return Status{}, e
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Status{}, context.Canceled
	}
	if task := s.tasks[id]; task != nil {
		state := task.state
		s.mu.Unlock()
		return Status{p.Info(), state}, nil
	}
	delete(s.outcomes, id)
	taskCtx, cancel := context.WithCancel(context.Background())
	task := &prepareTask{ctx: taskCtx, cancel: cancel, state: Preparation{Phase: "downloading"}, done: make(chan struct{})}
	winding := s.winding[id]
	s.tasks[id] = task
	s.mu.Unlock()
	go s.runPrepare(p, task, winding)
	return Status{p.Info(), Preparation{Phase: "downloading"}}, nil
}
func (s *Service) runPrepare(p Provider, task *prepareTask, winding <-chan struct{}) {
	defer func() {
		s.mu.Lock()
		if s.winding[p.Info().ID] == task.done {
			delete(s.winding, p.Info().ID)
		}
		close(task.done)
		s.mu.Unlock()
	}()
	id := p.Info().ID
	final := Preparation{Phase: "cancelled"}
	var err error
	if winding != nil {
		select {
		case <-winding:
		case <-task.ctx.Done():
			return
		}
	}
	if task.ctx.Err() != nil {
		return
	}
	disk, e := p.Status(task.ctx)
	if e != nil {
		err = e
	} else if disk.Phase == "ready" {
		final = disk
	} else {
		preferences, e := s.o.ReadPreferences(task.ctx)
		if e != nil {
			err = e
		} else {
			err = p.Prepare(task.ctx, preferences.String("downloadSource"), func(state Preparation) {
				s.mu.Lock()
				if s.tasks[id] == task {
					task.state = state
				}
				s.mu.Unlock()
			})
			if err == nil {
				after, e := p.Status(task.ctx)
				err = e
				if after.Phase == "ready" {
					final = after
				} else {
					s.mu.Lock()
					reported := task.state
					s.mu.Unlock()
					if reported.Phase == "failed" {
						final = reported
					} else {
						final = Preparation{Phase: "failed", Error: &Failure{Reason: "unknown", Message: "Preparation finished but the assets are not ready"}}
					}
				}
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tasks[id] != task {
		return
	}
	if err != nil {
		if task.ctx.Err() != nil {
			final = Preparation{Phase: "cancelled"}
		} else if task.state.Phase == "failed" {
			final = task.state
		} else {
			final = Preparation{Phase: "failed", Error: &Failure{Reason: "unknown", Message: err.Error()}}
		}
	}
	delete(s.tasks, id)
	if final.Phase == "ready" {
		delete(s.outcomes, id)
	} else {
		s.outcomes[id] = final
	}
	if s.winding[id] == task.done {
		delete(s.winding, id)
	}
}
func (s *Service) Cancel(ctx context.Context, id string) (Status, error) {
	p, e := s.require(id)
	if e != nil {
		return Status{}, e
	}
	s.mu.Lock()
	if task := s.tasks[id]; task != nil {
		delete(s.tasks, id)
		s.outcomes[id] = Preparation{Phase: "cancelled"}
		s.winding[id] = task.done
		task.cancel()
	}
	s.mu.Unlock()
	return s.Status(ctx, p.Info().ID)
}
func (s *Service) RemoveAssets(ctx context.Context, id string) (Status, error) {
	p, e := s.require(id)
	if e != nil {
		return Status{}, e
	}
	if _, e = s.Cancel(ctx, id); e != nil {
		return Status{}, e
	}
	s.mu.Lock()
	winding := s.winding[id]
	s.mu.Unlock()
	if winding != nil {
		select {
		case <-winding:
		case <-ctx.Done():
			return Status{}, context.Cause(ctx)
		}
	}
	removing := make(chan struct{})
	s.mu.Lock()
	delete(s.outcomes, id)
	s.winding[id] = removing
	s.mu.Unlock()
	e = p.Remove(ctx)
	close(removing)
	s.mu.Lock()
	if s.winding[id] == removing {
		delete(s.winding, id)
	}
	s.mu.Unlock()
	if e != nil {
		return Status{}, failure("voice/failed", "Failed to remove voice assets: "+e.Error())
	}
	return s.Status(ctx, id)
}
func (s *Service) Transcribe(ctx context.Context, id *string, wav []byte, language *string) (Transcript, error) {
	preferences, e := s.o.ReadPreferences(ctx)
	if e != nil {
		return Transcript{}, e
	}
	selected := preferences.String("providerId")
	if id != nil {
		selected = *id
	}
	p, e := s.require(selected)
	if e != nil {
		return Transcript{}, e
	}
	effective := providerLanguage(p, preferences.String("language"))
	if language != nil {
		if !contains(p.Info().Languages, *language) {
			return Transcript{}, failure("BAD_REQUEST", p.Info().Name+" does not support language: "+*language)
		}
		effective = *language
	}
	seconds, e := ValidateWAV(wav)
	if e != nil {
		return Transcript{}, e
	}
	state, e := s.preparation(ctx, p)
	if e != nil {
		return Transcript{}, e
	}
	if state.Phase != "ready" {
		return Transcript{}, failure("voice/not-ready", p.Info().Name+" is not ready ("+state.Phase+")")
	}
	result, e := p.Transcribe(ctx, wav, effective)
	if e != nil {
		var own *Error
		if ctx.Err() != nil || errors.As(e, &own) {
			return result, e
		}
		return result, failure("voice/failed", "Transcription failed: "+e.Error())
	}
	result.AudioSeconds = seconds
	return result, nil
}
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	tasks := []*prepareTask{}
	for _, task := range s.tasks {
		task.cancel()
		tasks = append(tasks, task)
	}
	winding := []<-chan struct{}{}
	for _, done := range s.winding {
		winding = append(winding, done)
	}
	s.mu.Unlock()
	for _, done := range winding {
		select {
		case <-done:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	for _, task := range tasks {
		select {
		case <-task.done:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	var errs []error
	for _, p := range s.o.Providers {
		errs = append(errs, p.Close(ctx))
	}
	return errors.Join(errs...)
}
