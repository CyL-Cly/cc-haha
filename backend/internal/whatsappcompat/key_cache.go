package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"
)

type nodeKeyCacheEntry struct {
	value   any
	expires time.Time
}

// NodeSignalKeyCache shares values, as makeCacheableSignalKeyStore useClones:false.
// A missing disk value is omitted; a cached null written by Set remains present.
type NodeSignalKeyCache struct {
	mu    sync.Mutex
	Now   func() time.Time
	Read  func(context.Context, string, []string) (map[string]any, error)
	Write func(context.Context, map[string]map[string]any) error
	cache map[string]nodeKeyCacheEntry
}

func (n *NodeSignalKeyCache) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}
func (n *NodeSignalKeyCache) Get(ctx context.Context, category string, ids []string) (map[string]any, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	out := map[string]any{}
	missing := []string{}
	for _, id := range ids {
		key := category + "." + id
		if v, ok := n.cache[key]; ok && n.now().Before(v.expires) {
			out[id] = v.value
		} else {
			delete(n.cache, key)
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		values, e := n.Read(ctx, category, missing)
		if e != nil {
			return nil, e
		}
		for _, id := range missing {
			value := values[id]
			if nodeCacheTruthy(value) {
				out[id] = value
				if n.cache == nil {
					n.cache = map[string]nodeKeyCacheEntry{}
				}
				n.cache[category+"."+id] = nodeKeyCacheEntry{value, n.now().Add(5 * time.Minute)}
			}
		}
	}
	return out, nil
}
func (n *NodeSignalKeyCache) Set(ctx context.Context, values map[string]map[string]any) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if n.cache == nil {
		n.cache = map[string]nodeKeyCacheEntry{}
	}
	for category, entries := range values {
		for id, value := range entries {
			n.cache[category+"."+id] = nodeKeyCacheEntry{value, n.now().Add(5 * time.Minute)}
		}
	}
	return n.Write(ctx, values)
}
func (n *NodeSignalKeyCache) Clear() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cache = map[string]nodeKeyCacheEntry{}
}

func nodeCacheTruthy(v any) bool {
	switch value := v.(type) {
	case json.RawMessage:
		data := bytes.TrimSpace(value)
		return len(data) > 0 && !bytes.Equal(data, []byte("null")) && !bytes.Equal(data, []byte("false")) && !bytes.Equal(data, []byte("0")) && !bytes.Equal(data, []byte(`""`))
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case int:
		return value != 0
	case float64:
		return value != 0
	default:
		return true
	}
}
