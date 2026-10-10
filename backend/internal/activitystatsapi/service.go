// Package activitystatsapi ports the desktop activity ledger. It reads the
// original transcripts and v7 derived cache; no provider or credentials enter it.
package activitystatsapi

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/dop251/goja"
)

type IndexGateway interface {
	Mode() string
	ActivityScopeReady() bool
	ActivityStats(string, time.Time) (*Stats, error)
}
type Options struct {
	HomeDir, ConfigDir string
	Now                func() time.Time
	Location           *time.Location
	ShotStatsEnabled   bool
	Index              IndexGateway
	OnDebug            func(string)
}
type Stats struct{ Data map[string]any }

func (stats Stats) MarshalJSON() ([]byte, error) { return json.Marshal(clean(stats.Data)) }

type pending struct {
	done  chan struct{}
	stats Stats
	err   error
}
type Service struct {
	options   Options
	mu        sync.Mutex
	running   map[string]*pending
	compute   sync.Mutex
	dateVM    *goja.Runtime
	cacheLock *sync.Mutex
}

var cacheLocks sync.Map

func New(options Options) (*Service, error) {
	if !filepath.IsAbs(options.HomeDir) || !filepath.IsAbs(options.ConfigDir) {
		return nil, errors.New("Activity stats require explicit absolute home/config paths")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Location == nil {
		options.Location = time.Local
	}
	lock, _ := cacheLocks.LoadOrStore(filepath.Clean(options.ConfigDir), &sync.Mutex{})
	return &Service{options: options, running: map[string]*pending{}, dateVM: goja.New(), cacheLock: lock.(*sync.Mutex)}, nil
}
func (s *Service) Now() time.Time { return s.options.Now() }
func (s *Service) Aggregate(ctx context.Context, rangeName string, now time.Time) (Stats, error) {
	if rangeName != "all" && rangeName != "7d" && rangeName != "30d" {
		return Stats{}, errors.New("Unknown activity stats range: " + rangeName)
	}
	key := rangeName + "\x00" + dateKey(now)
	s.mu.Lock()
	if existing := s.running[key]; existing != nil {
		s.mu.Unlock()
		select {
		case <-existing.done:
			return existing.stats, existing.err
		case <-ctx.Done():
			return Stats{}, ctx.Err()
		}
	}
	job := &pending{done: make(chan struct{})}
	s.running[key] = job
	s.mu.Unlock()
	s.compute.Lock()
	stats, err := s.aggregateMode(ctx, rangeName, now)
	s.compute.Unlock()
	s.mu.Lock()
	job.stats, job.err = stats, err
	delete(s.running, key)
	close(job.done)
	s.mu.Unlock()
	return stats, err
}
func (s *Service) aggregateMode(ctx context.Context, rangeName string, now time.Time) (Stats, error) {
	index := s.options.Index
	mode := "off"
	ready := false
	if index != nil {
		mode = index.Mode()
		ready = index.ActivityScopeReady()
	}
	compatible := true
	if rangeName == "all" && ready && (mode == "on" || mode == "shadow") {
		cache := s.loadCache()
		compatible = get(cache, "lastComputedDate") == nil
	}
	if mode == "on" && ready && compatible {
		if stats, err := index.ActivityStats(rangeName, now); err != nil {
			return Stats{}, err
		} else if stats != nil {
			return *stats, nil
		}
	}
	stats, err := s.aggregateFiles(ctx, rangeName, now)
	if err != nil {
		return Stats{}, err
	}
	if mode == "shadow" && ready && compatible {
		indexed, err := index.ActivityStats(rangeName, now)
		if err != nil {
			return Stats{}, err
		}
		if indexed != nil && string(encode(indexed.Data)) != string(encode(stats.Data)) && s.options.OnDebug != nil {
			s.options.OnDebug("Local activity index parity mismatch for range " + rangeName)
		}
	}
	return stats, nil
}
func (s *Service) aggregateFiles(ctx context.Context, rangeName string, now time.Time) (Stats, error) {
	files, err := s.allFiles()
	if err != nil {
		return Stats{}, err
	}
	if len(files) == 0 {
		return Stats{emptyStats()}, nil
	}
	if rangeName != "all" {
		days := 30
		if rangeName == "7d" {
			days = 7
		}
		from := dateKey(now.UTC().AddDate(0, 0, -days+1))
		to := dateKey(now)
		data, err := s.process(ctx, files, from, to)
		if err != nil {
			return Stats{}, err
		}
		return s.processedStats(data, now)
	}
	s.cacheLock.Lock()
	cache, err := s.updateHistoricalCache(ctx, files)
	s.cacheLock.Unlock()
	if err != nil {
		return Stats{}, err
	}
	today := dateKey(s.options.Now())
	data, err := s.process(ctx, files, today, today)
	if err != nil {
		return Stats{}, err
	}
	return s.cacheStats(cache, data, s.options.Now())
}
func (s *Service) updateHistoricalCache(ctx context.Context, files []string) (object, error) {
	cache := s.loadCache()
	clock := s.options.Now()
	yesterday := dateKey(clock.UTC().AddDate(0, 0, -1))
	if !truthy(get(cache, "lastComputedDate")) {
		history, err := s.process(ctx, files, "", yesterday)
		if err != nil {
			return nil, err
		}
		if len(history.sessions) > 0 || len(history.daily) > 0 {
			cache, err = s.mergeCache(cache, history, yesterday)
			if err != nil {
				return nil, err
			}
			s.saveCache(cache)
		}
	} else if last, ok := get(cache, "lastComputedDate").(string); ok && last < yesterday {
		date, valid, err := s.date(last + "T00:00:00.000Z")
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, errors.New("Invalid time value")
		}
		data, err := s.process(ctx, files, dateKey(date.UTC().AddDate(0, 0, 1)), yesterday)
		if err != nil {
			return nil, err
		}
		if len(data.sessions) > 0 || len(data.daily) > 0 {
			cache, err = s.mergeCache(cache, data, yesterday)
			if err != nil {
				return nil, err
			}
		} else {
			cache = spread(cache)
			cache["lastComputedDate"] = yesterday
		}
		s.saveCache(cache)
	}
	return cache, nil
}
