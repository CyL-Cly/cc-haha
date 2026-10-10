package mcpclient

import (
	"context"
	"errors"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type notificationSubscription struct {
	id       uint64
	callback func(string, protocol.JSONObject)
}

// SubscribeNotifications borrows this live client's actual notification
// stream. Unsubscription changes only this observer; it does not reset the
// transport or replace the existing progress/cancellation/host handlers.
func (c *Client) SubscribeNotifications(callback func(string, protocol.JSONObject)) (func(), error) {
	if c == nil || callback == nil {
		return nil, errors.New("MCP notification observer requires its live client and callback")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, context.Canceled
	}
	c.notificationID++
	id := c.notificationID
	c.notificationSubscriptions = append(c.notificationSubscriptions, notificationSubscription{id: id, callback: callback})
	c.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			for i, subscription := range c.notificationSubscriptions {
				if subscription.id == id {
					c.notificationSubscriptions = append(c.notificationSubscriptions[:i], c.notificationSubscriptions[i+1:]...)
					return
				}
			}
		})
	}, nil
}

func (c *Client) dispatchNotificationObservers(method string, params protocol.JSONObject) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	callbacks := append([]notificationSubscription(nil), c.notificationSubscriptions...)
	c.mu.Unlock()
	for _, subscription := range callbacks {
		subscription.callback(method, params.Clone())
	}
}
