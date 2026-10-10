package insights

import (
	"encoding/json"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// These batches operate only on physical independent files and return settled
// immutable data. The report's owning adapter awaits their completion; session
// metadata, models, facets, and report state stay on that Owner.
func (c *Cache) loadBatch(ids []string, facets bool) []json.RawMessage {
	values := make([]json.RawMessage, len(ids))
	var workers sync.WaitGroup
	for i, id := range ids {
		workers.Add(1)
		go func(i int, id string) {
			defer workers.Done()
			if facets {
				values[i] = c.LoadFacets(id)
			} else {
				values[i] = c.LoadMeta(id)
			}
		}(i, id)
	}
	workers.Wait()
	return values
}
func (c *Cache) saveBatch(values []protocol.JSONObject, facets bool) error {
	failures := make([]error, len(values))
	var workers sync.WaitGroup
	for i, value := range values {
		data := raw(value)
		workers.Add(1)
		go func(i int, data json.RawMessage) {
			defer workers.Done()
			copy, err := object(data)
			if err != nil {
				failures[i] = err
				return
			}
			if facets {
				failures[i] = c.SaveFacets(copy)
			} else {
				failures[i] = c.SaveMeta(copy)
			}
		}(i, data)
	}
	workers.Wait()
	for _, err := range failures {
		if err != nil {
			return err
		}
	}
	return nil
}
