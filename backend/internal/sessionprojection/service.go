// Package sessionprojection reduces transcript metadata into the server's
// persisted launch and list projections. It does not locate sessions or resume
// an Agent. The caller owns its actual stat adapter and shared history budget.
package sessionprojection

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"unicode/utf8"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type Stat struct {
	SourceVersion string
	BirthtimeISO  string
	MtimeISO      string
}
type DateParser func([]uint16) (milliseconds float64, valid bool, err error)

var ErrUnsupportedPath = errors.New("session projection requires valid Unicode file and project paths")

type Options struct {
	Reader    *sessionmetadata.Reader
	Budget    *sessionstore.Budget
	Stat      func(string) (Stat, error)
	ConfigDir func() string
	Cwd       func() string
	Platform  string
	ParseDate DateParser
}
type Result struct {
	Signature   string          `json:"signature,omitempty"`
	Summary     json.RawMessage `json:"summary"`
	LaunchInfo  json.RawMessage `json:"launchInfo"`
	CustomTitle json.RawMessage `json:"customTitle"`
}
type cached struct {
	key       string
	signature string
	value     Result
}
type request struct {
	done  chan struct{}
	value Result
	err   error
}
type Service struct {
	options  Options
	mu       sync.Mutex
	cache    map[string]*list.Element
	lru      *list.List
	requests map[string]*request
}

func New(options Options) (*Service, error) {
	if options.Reader == nil || options.Budget == nil || options.Stat == nil || options.ConfigDir == nil || options.Cwd == nil || options.ParseDate == nil {
		return nil, errors.New("session projection requires reader, shared budget, stat, config, cwd and date parser")
	}
	if options.Platform != "darwin" && options.Platform != "linux" && options.Platform != "win32" {
		return nil, errors.New("session projection requires a supported explicit platform")
	}
	return &Service{options: options, cache: map[string]*list.Element{}, lru: list.New(), requests: map[string]*request{}}, nil
}
func clone(value Result) Result {
	value.Summary = append(json.RawMessage(nil), value.Summary...)
	value.LaunchInfo = append(json.RawMessage(nil), value.LaunchInfo...)
	value.CustomTitle = append(json.RawMessage(nil), value.CustomTitle...)
	return value
}

// Get preserves Source getMetadataProjection's non-cancellable shared build.
// Stat is performed even on a cache hit. Config scope and file signature are
// part of the cache/request identities, while projectDir is not.
func (service *Service) Get(filePath, projectDir string) (Result, error) {
	if !utf8.ValidString(filePath) || !utf8.ValidString(projectDir) {
		return Result{}, ErrUnsupportedPath
	}
	stat, err := service.options.Stat(filePath)
	if err != nil {
		return Result{}, err
	}
	key := service.options.ConfigDir() + "\x00" + filePath
	requestKey := key + "\x00" + stat.SourceVersion
	service.mu.Lock()
	if item := service.cache[key]; item != nil && item.Value.(cached).signature == stat.SourceVersion {
		service.lru.MoveToBack(item)
		value := clone(item.Value.(cached).value)
		value.Signature = stat.SourceVersion
		service.mu.Unlock()
		return value, nil
	}
	if pending := service.requests[requestKey]; pending != nil {
		service.mu.Unlock()
		<-pending.done
		return clone(pending.value), pending.err
	}
	pending := &request{done: make(chan struct{})}
	service.requests[requestKey] = pending
	service.mu.Unlock()
	var version string
	err = service.options.Budget.With(context.Background(), sessionstore.MetadataLane, func() error {
		launch, summary := newState(), newState()
		scan, err := service.options.Reader.Stream(context.Background(), filePath, func(entry sessionmetadata.Entry, complete bool, _ int64) error {
			var raw protocol.JSONObject
			if err := json.Unmarshal(entry.Value, &raw); err != nil {
				return err
			}
			if err := service.apply(&launch, raw); err != nil {
				return err
			}
			if complete {
				return service.apply(&summary, raw)
			}
			return nil
		}, sessionmetadata.Options{})
		if err != nil {
			return err
		}
		version = scan.SourceVersion
		pending.value = service.result(launch, summary, filePath, projectDir, stat)
		return nil
	})
	service.mu.Lock()
	if err == nil {
		if old := service.cache[key]; old != nil {
			service.lru.Remove(old)
		}
		service.cache[key] = service.lru.PushBack(cached{key, version, clone(pending.value)})
		for service.lru.Len() > 32 {
			first := service.lru.Front()
			delete(service.cache, first.Value.(cached).key)
			service.lru.Remove(first)
		}
	}
	pending.err = err
	delete(service.requests, requestKey)
	close(pending.done)
	service.mu.Unlock()
	return clone(pending.value), err
}

// PersistedLaunchInfo implements only the already-located persisted branch of
// getSessionLaunchInfo. Locator, side-chat and no-transcript registries remain
// caller responsibilities. Memory fields override persisted fields, except the
// actual transcriptMessageCount. No transcript is rewritten here.
func (service *Service) PersistedLaunchInfo(filePath, projectDir string, memory json.RawMessage) (json.RawMessage, error) {
	result, err := service.Get(filePath, projectDir)
	if err != nil {
		return nil, err
	}
	var launch protocol.JSONObject
	if err := json.Unmarshal(result.LaunchInfo, &launch); err != nil {
		return nil, err
	}
	count := launch.Get("transcriptMessageCount")
	if memory != nil {
		var fields protocol.JSONObject
		if err := json.Unmarshal(memory, &fields); err != nil {
			return nil, err
		}
		encoded, err := fields.MarshalJSON()
		if err != nil {
			return nil, err
		}
		// The shared parser resolves duplicate keys in their original slots,
		// retaining lone-surrogate keys that encoding/json Token would replace.
		original, _ := launch.MarshalJSON()
		if encoded = bytes.TrimSpace(encoded); len(encoded) > 2 && encoded[0] == '{' {
			merged := append(append(append([]byte(nil), original[:len(original)-1]...), ','), encoded[1:len(encoded)-1]...)
			merged = append(merged, '}')
			if err := json.Unmarshal(merged, &launch); err != nil {
				return nil, err
			}
		}
	}
	launch.Set("transcriptMessageCount", count)
	return launch.MarshalJSON()
}
