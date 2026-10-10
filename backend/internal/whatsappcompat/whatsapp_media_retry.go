package whatsappcandidate

import (
	"context"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waMmsRetry"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// UpdateMedia waits for the real encrypted phone notification. Registering the
// waiter before the receipt prevents a fast response from being lost.
func (n *NodeWhatsAppClient) UpdateMedia(ctx context.Context, info types.MessageInfo, key []byte) (string, error) {
	response := make(chan *events.MediaRetry, 1)
	n.mu.Lock()
	if n.closed || n.terminal {
		n.mu.Unlock()
		return "", errors.New("Connection Closed")
	}
	n.mediaRetries[info.ID] = append(n.mediaRetries[info.ID], response)
	n.wg.Add(1)
	n.mu.Unlock()
	defer func() {
		n.mu.Lock()
		waiters := n.mediaRetries[info.ID]
		for i, ch := range waiters {
			if ch == response {
				waiters = append(waiters[:i], waiters[i+1:]...)
				break
			}
		}
		if len(waiters) == 0 {
			delete(n.mediaRetries, info.ID)
		} else {
			n.mediaRetries[info.ID] = waiters
		}
		n.mu.Unlock()
		n.wg.Done()
	}()
	if e := n.Client.SendMediaRetryReceipt(ctx, &info, key); e != nil {
		return "", e
	}
	var event *events.MediaRetry
	select {
	case event = <-response:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-n.ctx.Done():
		return "", n.ctx.Err()
	}
	if event == nil {
		return "", errors.New("Connection Closed")
	}
	if event.Error != nil {
		return "", fmt.Errorf("Failed to re-upload media (%d)", event.Error.Code)
	}
	result, e := whatsmeow.DecryptMediaRetryNotification(event, key)
	if e != nil {
		return "", e
	}
	if result.GetResult() != waMmsRetry.MediaRetryNotification_SUCCESS {
		return "", fmt.Errorf("Media re-upload failed by device (%s)", result.GetResult().String())
	}
	return result.GetDirectPath(), nil
}

func (n *NodeWhatsAppClient) deliverMediaRetry(value any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch event := value.(type) {
	case *events.MediaRetry:
		for _, ch := range n.mediaRetries[event.MessageID] {
			select {
			case ch <- event:
			default:
			}
		}
	case *events.Disconnected, *events.LoggedOut:
		for _, waiters := range n.mediaRetries {
			for _, ch := range waiters {
				select {
				case ch <- nil:
				default:
				}
			}
		}
	}
}
