package remoteio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Ingress is the original v1 optimistic transcript log protocol. It shares
// native auth with RemoteIO, but its per-session Last-Uuid chain is independent
// of SDK message UUID replay and CCR v2 worker epochs.
type Ingress struct {
	HTTP                *http.Client
	Auth                *IngressAuth
	AfterLastCompact    func() bool
	Wait                func(context.Context, time.Duration) error
	mu                  sync.Mutex
	sessions            map[string]*ingressSession
	compactSet, compact bool
}

func (i *Ingress) SetAfterLastCompact(value bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.compactSet, i.compact = true, value
}
func (i *Ingress) afterCompact() bool {
	i.mu.Lock()
	set, value := i.compactSet, i.compact
	i.mu.Unlock()
	if set {
		return value
	}
	return i.AfterLastCompact != nil && i.AfterLastCompact()
}

type ingressSession struct {
	mu   sync.Mutex
	last string
}

func NewIngress(client *http.Client, auth *IngressAuth, afterLastCompact func() bool) *Ingress {
	if client == nil {
		client = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	}
	return &Ingress{HTTP: client, Auth: auth, AfterLastCompact: afterLastCompact, Wait: wait, sessions: map[string]*ingressSession{}}
}
func (i *Ingress) session(sid string) *ingressSession {
	i.mu.Lock()
	defer i.mu.Unlock()
	session := i.sessions[sid]
	if session == nil {
		session = &ingressSession{}
		i.sessions[sid] = session
	}
	return session
}
func (i *Ingress) read(ctx context.Context, target, token string) ([]json.RawMessage, error) {
	if token == "" {
		return nil, nil
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, nil
	}
	if i.afterCompact() {
		query := u.Query()
		query.Set("after_last_compact", "true")
		u.RawQuery = query.Encode()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := i.HTTP.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return []json.RawMessage{}, nil
	}
	if response.StatusCode != 200 {
		return nil, nil
	}
	var body struct {
		Loglines []json.RawMessage `json:"loglines"`
	}
	var rawBody protocol.JSONObject
	data, err := io.ReadAll(response.Body)
	if err != nil || rawBody.UnmarshalJSON(data) != nil || len(rawBody.Get("loglines")) == 0 || rawBody.Get("loglines")[0] != '[' {
		return nil, nil
	}
	if json.Unmarshal(data, &body) != nil {
		return nil, nil
	}
	return body.Loglines, nil
}
func (i *Ingress) Read(ctx context.Context, sid, target string) ([]json.RawMessage, error) {
	rows, err := i.read(ctx, target, i.Auth.Token())
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		last := object(rows[len(rows)-1])
		if !last.IsObject() {
			return nil, errors.New("session log's last entry is not an object")
		}
		if value := last.String("uuid"); value != "" {
			session := i.session(sid)
			session.mu.Lock()
			session.last = value
			session.mu.Unlock()
		}
	}
	return rows, nil
}
func (i *Ingress) Append(ctx context.Context, sid, target string, entry protocol.JSONObject) (bool, error) {
	token := i.Auth.Token()
	if token == "" {
		return false, nil
	}
	session := i.session(sid)
	session.mu.Lock()
	defer session.mu.Unlock()
	for attempt := 1; attempt <= 10; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(raw(entry)))
		if err != nil {
			return false, nil
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		if session.last != "" {
			request.Header.Set("Last-Uuid", session.last)
		}
		response, err := i.HTTP.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			switch response.StatusCode {
			case 200, 201:
				session.last = entry.String("uuid")
				return true, nil
			case 401:
				return false, nil
			case 409:
				last := response.Header.Get("X-Last-Uuid")
				if last == entry.String("uuid") {
					session.last = last
					return true, nil
				}
				if last == "" {
					rows, readErr := i.read(ctx, target, token)
					if readErr != nil {
						return false, readErr
					}
					for index := len(rows) - 1; index >= 0; index-- {
						if value := object(rows[index]).String("uuid"); value != "" {
							last = value
							break
						}
					}
				}
				if last == "" {
					return false, nil
				}
				session.last = last
				continue
			}
		} else if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if attempt < 10 {
			delay := 500 * time.Millisecond
			for index := 1; index < attempt && delay < 8*time.Second; index++ {
				delay *= 2
			}
			if delay > 8*time.Second {
				delay = 8 * time.Second
			}
			if err = i.Wait(ctx, delay); err != nil {
				return false, err
			}
		}
	}
	return false, nil
}

func (i *Ingress) Close() {
	if i != nil {
		if client, ok := i.HTTP.Transport.(interface{ CloseIdleConnections() }); ok {
			client.CloseIdleConnections()
		}
	}
}
