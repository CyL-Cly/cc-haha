package builtintools

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func taskOutputDelta(path string, offset int64) (bool, int64, error) {
	f, e := os.Open(path)
	if e != nil {
		return false, offset, nil
	}
	defer f.Close()
	if _, e = f.Seek(offset, io.SeekStart); e != nil {
		return false, offset, nil
	}
	data, e := io.ReadAll(io.LimitReader(f, 8*1024*1024))
	if e != nil {
		return false, offset, nil
	}
	return len(data) > 0, offset + int64(len(data)), nil
}
func (s *Service) PollBackgroundTasks(scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	if !s.HasBackgroundTaskBindings() {
		return nil
	}
	b := s.options.BackgroundTasks
	tasks, e := b.List(scope)
	if e != nil {
		return e
	}
	offsets := map[string]int64{}
	evicted := []string{}
	for _, task := range tasks {
		id := task.String("id")
		status := task.String("status")
		if boolean(task, "notified", false) {
			if status == "completed" || status == "failed" || status == "killed" {
				evicted = append(evicted, id)
				continue
			}
			if status == "pending" {
				continue
			}
		}
		if status == "running" {
			live, e := b.Lookup(scope, id)
			if e != nil {
				return e
			}
			if live == nil || live.ReadDelta == nil {
				continue
			}
			var offset int64
			_ = json.Unmarshal(task.Get("outputOffset"), &offset)
			type delta struct {
				HasContent bool
				Offset     int64
			}
			f, e := native(s, scope, func() (delta, error) {
				has, next, e := live.ReadDelta(context.Background(), offset)
				return delta{has, next}, e
			})
			if e != nil {
				return e
			}
			value, e := f.Await(scope)
			if e != nil {
				return e
			}
			if value.HasContent {
				offsets[id] = value.Offset
			}
		}
	}
	for id, offset := range offsets {
		if e = b.Update(scope, id, func(task protocol.JSONObject) (protocol.JSONObject, error) {
			if task.String("status") == "running" {
				task.Set("outputOffset", raw(offset))
			}
			return task, nil
		}); e != nil {
			return e
		}
	}
	for _, id := range evicted {
		fresh, e := b.Lookup(scope, id)
		if e != nil {
			return e
		}
		if fresh == nil {
			continue
		}
		status := fresh.Data.String("status")
		if status != "completed" && status != "failed" && status != "killed" || !boolean(fresh.Data, "notified", false) {
			continue
		}
		if fresh.Data.Get("retain") != nil {
			var after float64
			if fresh.Data.Get("evictAfter") == nil || json.Unmarshal(fresh.Data.Get("evictAfter"), &after) != nil || after > s.options.NowMillis() {
				continue
			}
		}
		if b.Delete == nil {
			return capability("actual task eviction")
		}
		if e = b.Delete(scope, id); e != nil {
			return e
		}
	}
	return nil
}
