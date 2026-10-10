package whatsappcandidate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type nodeLIDCacheEntry struct {
	value   string
	expires time.Time
}
type NodeLIDStore struct {
	Dir   string
	Now   func() time.Time
	mu    sync.Mutex
	cache map[string]nodeLIDCacheEntry
}

func (n *NodeLIDStore) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}
func (n *NodeLIDStore) get(name string) string {
	if n.cache == nil {
		n.cache = map[string]nodeLIDCacheEntry{}
	}
	v := n.cache[name]
	if v.value != "" && n.now().Before(v.expires) {
		v.expires = n.now().Add(72 * time.Hour)
		n.cache[name] = v
		return v.value
	}
	delete(n.cache, name)
	return ""
}
func (n *NodeLIDStore) put(name, value string) {
	if n.cache == nil {
		n.cache = map[string]nodeLIDCacheEntry{}
	}
	n.cache[name] = nodeLIDCacheEntry{value, n.now().Add(72 * time.Hour)}
}
func (n *NodeLIDStore) read(id string) string {
	data, e := os.ReadFile(filepath.Join(n.Dir, "lid-mapping-"+strings.ReplaceAll(strings.ReplaceAll(id, "/", "__"), ":", "-")+".json"))
	if e != nil {
		return ""
	}
	return nodeString(data)
}
func (n *NodeLIDStore) write(id, value string) error {
	return os.WriteFile(filepath.Join(n.Dir, "lid-mapping-"+strings.ReplaceAll(strings.ReplaceAll(id, "/", "__"), ":", "-")+".json"), nodeJSON(value), 0666)
}
func nodeJIDParts(jid string) (user, device, server string, ok bool) {
	p := strings.SplitN(jid, "@", 2)
	if len(p) != 2 {
		return
	}
	server = p[1]
	pair := strings.SplitN(p[0], ":", 2)
	user = strings.SplitN(pair[0], "_", 2)[0]
	device = "0"
	if len(pair) == 2 {
		device = pair[1]
	}
	return user, device, server, true
}
func (n *NodeLIDStore) StoreLIDPNMappings(ctx context.Context, pairs [][2]string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	updates := map[string]string{}
	order := []string{}
	for _, pair := range pairs {
		lid, pn := pair[0], pair[1]
		if !((strings.HasSuffix(lid, "@lid") && strings.HasSuffix(pn, "@s.whatsapp.net")) || (strings.HasSuffix(lid, "@s.whatsapp.net") && strings.HasSuffix(pn, "@lid"))) {
			continue
		}
		pu, _, _, ok := nodeJIDParts(pn)
		if !ok {
			return nil
		}
		lu, _, _, ok := nodeJIDParts(lid)
		if !ok {
			return nil
		}
		existing := n.get("pn:" + pu)
		if existing == "" {
			existing = n.read(pu)
			if existing != "" {
				n.put("pn:"+pu, existing)
				n.put("lid:"+existing, pu)
			}
		}
		if existing == lu {
			continue
		}
		if _, exists := updates[pu]; !exists {
			order = append(order, pu)
		}
		updates[pu] = lu
	}
	for _, pu := range order {
		lu := updates[pu]
		if e := n.write(pu, lu); e != nil {
			return e
		}
		if e := n.write(lu+"_reverse", pu); e != nil {
			return e
		}
		n.put("pn:"+pu, lu)
		n.put("lid:"+lu, pu)
	}
	return nil
}
func (n *NodeLIDStore) GetLIDForPN(ctx context.Context, pn string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return "", e
	}
	user, device, server, ok := nodeJIDParts(pn)
	if !ok || server != "s.whatsapp.net" && server != "hosted" {
		return "", nil
	}
	lid := n.get("pn:" + user)
	if lid == "" {
		lid = n.read(user)
		if lid != "" {
			n.put("pn:"+user, lid)
			n.put("lid:"+lid, user)
		}
	}
	if lid == "" {
		return "", nil
	}
	if device != "0" && device != "" {
		lid += ":" + device
	}
	if server == "hosted" {
		return lid + "@hosted.lid", nil
	}
	return lid + "@lid", nil
}
func (n *NodeLIDStore) GetPNForLID(ctx context.Context, lid string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return "", e
	}
	user, device, server, ok := nodeJIDParts(lid)
	if !ok || server != "lid" {
		return "", nil
	}
	pn := n.get("lid:" + user)
	if pn == "" {
		pn = n.read(user + "_reverse")
		if pn != "" {
			n.put("lid:"+user, pn)
		}
	}
	if pn == "" {
		return "", nil
	}
	return pn + ":" + device + "@s.whatsapp.net", nil
}
func (n *NodeLIDStore) Snapshot() map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	entries, _ := os.ReadDir(n.Dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "lid-mapping-") {
			v, _ := os.ReadFile(filepath.Join(n.Dir, e.Name()))
			out[e.Name()] = v
		}
	}
	return out
}
