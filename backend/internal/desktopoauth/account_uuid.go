package desktopoauth

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
	"time"
)

type accountUUIDLookup struct {
	done       chan struct{}
	value      string
	generation uint64
}

func (s *Service) forgetAccountUUID() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountUUID, s.accountRetryAt = "", 0
	s.accountGeneration++
	s.accountLookup = nil
}

// GetAccountUUID is best effort. A slow profile lookup cannot hold a session
// start beyond three seconds. A late answer still warms this account's cache;
// logout invalidates its generation and Close cancels and joins its transport.
func (s *Service) GetAccountUUID(ctx context.Context, token string) (*string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.accountClosed {
		s.mu.Unlock()
		return nil, nil
	}
	if s.accountUUID != "" {
		value := s.accountUUID
		s.mu.Unlock()
		return &value, nil
	}
	if s.options.Now().UnixMilli() < s.accountRetryAt {
		s.mu.Unlock()
		return nil, nil
	}
	s.mu.Unlock()
	stored, err := s.LoadTokens(ctx)
	if err != nil {
		return nil, nil
	}
	if stored != nil {
		var scopes []string
		raw := stored.Record().Get("scopes")
		_ = json.Unmarshal(raw, &scopes)
		found := false
		for _, scope := range scopes {
			if scope == "user:profile" {
				found = true
			}
		}
		var legacy string
		if json.Unmarshal(raw, &legacy) == nil {
			found = strings.Contains(legacy, "user:profile")
		}
		if !found {
			return nil, nil
		}
	}
	s.mu.Lock()
	if s.accountClosed {
		s.mu.Unlock()
		return nil, nil
	}
	if s.accountUUID != "" {
		value := s.accountUUID
		s.mu.Unlock()
		return &value, nil
	}
	if s.options.Now().UnixMilli() < s.accountRetryAt {
		s.mu.Unlock()
		return nil, nil
	}
	if s.accountLife == nil {
		s.accountLife, s.accountCancel = context.WithCancel(context.Background())
	}
	pending := s.accountLookup
	if pending == nil {
		pending = &accountUUIDLookup{done: make(chan struct{}), generation: s.accountGeneration}
		s.accountLookup = pending
		s.accountWorkers.Add(1)
		life := s.accountLife
		go func() {
			defer s.accountWorkers.Done()
			value, _ := s.fetchAccountUUID(life, token)
			s.mu.Lock()
			if !s.accountClosed && pending.generation == s.accountGeneration {
				pending.value = value
				if value != "" {
					s.accountUUID = value
				}
				if s.accountLookup == pending {
					s.accountLookup = nil
				}
			}
			close(pending.done)
			s.mu.Unlock()
		}()
	}
	wait := s.options.AccountLookupWait
	if wait <= 0 {
		wait = 3 * time.Second
	}
	s.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-timer.C:
	case <-pending.done:
		if pending.value != "" {
			value := pending.value
			return &value, nil
		}
	}
	s.mu.Lock()
	if pending.generation == s.accountGeneration && s.accountUUID == "" {
		s.accountRetryAt = s.options.Now().UnixMilli() + 60_000
	}
	s.mu.Unlock()
	return nil, nil
}

func (s *Service) fetchAccountUUID(ctx context.Context, token string) (string, error) {
	if s.options.FetchAccountUUID != nil {
		return s.options.FetchAccountUUID(ctx, token)
	}
	config, err := s.options.Config()
	if err != nil {
		return "", err
	}
	profile, _, err := s.request(ctx, "GET", config.BaseAPIURL+"/api/oauth/profile", protocol.JSONObject{}, token, 10*time.Second, true)
	if err != nil {
		return "", err
	}
	return object(profile.Get("account")).String("uuid"), nil
}
