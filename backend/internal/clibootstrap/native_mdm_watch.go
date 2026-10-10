package clibootstrap

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The process owns the native MDM/HKCU cache. Child agents borrow this pointer;
// a fresh physical poll is installed only by the original change notifier.
type nativeMDMCache struct {
	mu          sync.Mutex
	loaded      bool
	admin, user *protocol.JSONObject
	read        func(context.Context) (*protocol.JSONObject, *protocol.JSONObject, error)
}

func (cache *nativeMDMCache) current(ctx context.Context) (*protocol.JSONObject, *protocol.JSONObject, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !cache.loaded {
		admin, user, err := cache.read(ctx)
		if err != nil {
			return nil, nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, nil, err
		}
		cache.admin, cache.user, cache.loaded = admin, user, true
	}
	return cache.admin, cache.user, nil
}

func mdmSettingsSnapshot(admin, user *protocol.JSONObject) json.RawMessage {
	value := protocol.NewJSONObject()
	value.Set("mdm", []byte("{}"))
	value.Set("hkcu", []byte("{}"))
	if admin != nil {
		raw, _ := admin.MarshalJSON()
		value.Set("mdm", raw)
	}
	if user != nil {
		raw, _ := user.MarshalJSON()
		value.Set("hkcu", raw)
	}
	raw, _ := value.MarshalJSON()
	return protocol.StringifyValidatedJavaScriptJSON(raw)
}

func (n *NativeBoundary) settingsMDMSnapshot(ctx context.Context, refresh bool) (json.RawMessage, error) {
	if n.mdm == nil {
		return nil, mainCap("actual native MDM/HKCU cache reader")
	}
	var admin, user *protocol.JSONObject
	var err error
	if refresh {
		admin, user, err = n.mdm.read(ctx)
	} else {
		admin, user, err = n.mdm.current(ctx)
	}
	if err != nil {
		return nil, err
	}
	return mdmSettingsSnapshot(admin, user), nil
}

func (n *NativeBoundary) installSettingsMDMSnapshot(ctx context.Context, snapshot json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if n.mdm == nil {
		return mainCap("actual native MDM/HKCU cache updater")
	}
	var value protocol.JSONObject
	if err := value.UnmarshalJSON(snapshot); err != nil {
		return err
	}
	var admin, user *protocol.JSONObject
	for name, target := range map[string]**protocol.JSONObject{"mdm": &admin, "hkcu": &user} {
		raw := value.Get(name)
		if len(raw) == 0 {
			continue
		}
		var object protocol.JSONObject
		if err := object.UnmarshalJSON(raw); err != nil {
			return err
		}
		if len(object.OwnProperties()) > 0 {
			*target = &object
		}
	}
	n.mdm.mu.Lock()
	defer n.mdm.mu.Unlock()
	n.mdm.admin, n.mdm.user, n.mdm.loaded = admin, user, true
	return nil
}
