package remoteio

// ClearAllSessions drops process ingress heads and sequential wrapper caches.
// Already-admitted transports keep their own completion and cancellation.
func (i *Ingress) ClearAllSessions() {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	clear(i.heads)
	clear(i.sessions)
}
