package whatsappcandidate

import (
	"net/http"
	"net/http/httptest"
	"sync"
)

// mediaFixtureHandlers owns test-side work, including hijacked WebSocket
// handlers and their synchronously awaited actual Source fixture subprocesses.
// httptest.Server stops tracking a connection at StateHijacked, before its
// handler has necessarily returned.
type mediaFixtureHandlers struct {
	mu       sync.Mutex
	stopping bool
	active   sync.WaitGroup
	once     sync.Once
	done     chan struct{}
}

func newMediaFixtureHandlers() *mediaFixtureHandlers {
	return &mediaFixtureHandlers{done: make(chan struct{})}
}

func (handlers *mediaFixtureHandlers) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.mu.Lock()
		if handlers.stopping {
			handlers.mu.Unlock()
			http.Error(w, "fixture peer closed", http.StatusServiceUnavailable)
			return
		}
		handlers.active.Add(1)
		handlers.mu.Unlock()
		defer handlers.active.Done()
		next.ServeHTTP(w, r)
	})
}

// close first prevents any later handler from adding work. Once httptest has
// closed its ordinary connections, this receipt joins the separately owned
// handlers. The caller closes its client/cancels its peer read context first.
func (handlers *mediaFixtureHandlers) close(peer *httptest.Server) <-chan struct{} {
	handlers.once.Do(func() {
		handlers.mu.Lock()
		handlers.stopping = true
		handlers.mu.Unlock()
		peer.Close()
		go func() {
			handlers.active.Wait()
			close(handlers.done)
		}()
	})
	return handlers.done
}
