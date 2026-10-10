package taskstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) timestamp() string { return s.options.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

func sourceNumber(v string) float64 {
	v = trim(v)
	if v == "" {
		return 0
	}
	if len(v) > 2 && v[0] == '0' {
		base := 0
		switch v[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base != 0 {
			n, err := strconv.ParseUint(v[2:], base, 64)
			if err == nil {
				return float64(n)
			}
			return math.NaN()
		}
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil && !math.IsInf(n, 0) {
		return math.NaN()
	}
	return n
}
func (s *Service) revision(list string) (float64, error) {
	value, err := os.ReadFile(filepath.Join(s.tasksDir(list), ".revision"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	content := trim(string(value))
	if content == "" {
		return 0, fmt.Errorf("Invalid empty task-list revision for %s", list)
	}
	n := sourceNumber(content)
	if !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 9007199254740991 && math.Trunc(n) == n {
		return n, nil
	}
	return 0, fmt.Errorf("Invalid task-list revision for %s: %s", list, content)
}
func (s *Service) bumpRevision(list string) (float64, error) {
	n, err := s.revision(list)
	if err != nil {
		return 0, err
	}
	n++
	path := filepath.Join(s.tasksDir(list), ".revision")
	temporary := fmt.Sprintf("%s.%d.tmp", path, s.options.PID)
	if err := os.WriteFile(temporary, []byte(numberText(n)), 0666); err != nil {
		return 0, err
	}
	if err := os.Rename(temporary, path); err != nil {
		return 0, err
	}
	return n, nil
}
func (s *Service) highWaterMark(list string) float64 {
	value, err := os.ReadFile(filepath.Join(s.tasksDir(list), ".highwatermark"))
	if err != nil {
		return 0
	}
	n := decimalPrefix(string(value))
	if math.IsNaN(n) {
		return 0
	}
	return n
}
func (s *Service) highestID(list string) float64 {
	highest := 0.0
	files, err := os.ReadDir(s.tasksDir(list))
	if err == nil {
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".json") {
				n := decimalPrefix(strings.Replace(file.Name(), ".json", "", 1))
				if !math.IsNaN(n) && n > highest {
					highest = n
				}
			}
		}
	}
	return math.Max(highest, s.highWaterMark(list))
}

func (s *Service) Create(list string, data protocol.JSONObject) (result Creation, err error) {
	err = s.withLifecycle(list, true, func(lifecycle) error {
		return s.withList(list, func() error {
			id := numberText(s.highestID(list) + 1)
			task := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quote(id)})
			for _, property := range data.OwnProperties() {
				if err := task.SetPropertyJSON(property.NameJSON, property.Value); err != nil {
					return err
				}
			}
			revision, err := s.bumpRevision(list)
			if err != nil {
				return err
			}
			if err := s.writeTask(list, quote(id), task); err != nil {
				return err
			}
			result = Creation{id, s.timestamp(), revision}
			return nil
		})
	})
	return result, err
}

func stringsFromJSON(value json.RawMessage) []json.RawMessage {
	result := []json.RawMessage{}
	_ = json.Unmarshal(value, &result)
	return result
}
func contains(values []json.RawMessage, value json.RawMessage) bool {
	for _, existing := range values {
		if jsonEqual(existing, value) {
			return true
		}
	}
	return false
}
func unique(values []json.RawMessage) []json.RawMessage {
	result := []json.RawMessage{}
	for _, value := range values {
		if !contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}
func (s *Service) updateUnsafe(list string, id json.RawMessage, updates protocol.JSONObject) (*protocol.JSONObject, error) {
	current, err := s.Get(list, id)
	if err != nil || current == nil {
		return nil, err
	}
	for _, property := range updates.OwnProperties() {
		if err := current.SetPropertyJSON(property.NameJSON, property.Value); err != nil {
			return nil, err
		}
	}
	current.Set("id", id)
	if err := s.writeTask(list, id, *current); err != nil {
		return nil, err
	}
	return current, nil
}
func (s *Service) updateRelated(list string, id json.RawMessage, updates protocol.JSONObject) (err error) {
	task, err := s.Get(list, id)
	if err != nil || task == nil {
		return err
	}
	path, err := s.taskPath(list, id)
	if err != nil {
		return err
	}
	release, err := s.lock(path)
	if err != nil {
		return err
	}
	defer func() {
		if failure := release(); err == nil {
			err = failure
		}
	}()
	_, err = s.updateUnsafe(list, id, updates)
	return err
}

// Update applies TaskUpdate's conditional fields and reciprocal edges inside
// the original lifecycle -> list -> target lease order. Completion hooks run
// in the owner-bound tool before this method; the transaction re-reads state.
func (s *Service) Update(list string, id json.RawMessage, input protocol.JSONObject) (result *Mutation, err error) {
	return s.update(list, id, input, false)
}

func (s *Service) update(list string, id json.RawMessage, input protocol.JSONObject, rawUpdate bool, derive ...func(protocol.JSONObject) (*protocol.JSONObject, error)) (result *Mutation, err error) {
	err = s.withLifecycle(list, true, func(lifecycle) error {
		return s.withList(list, func() (err error) {
			current, err := s.Get(list, id)
			if err != nil || current == nil {
				return err
			}
			path, err := s.taskPath(list, id)
			if err != nil {
				return err
			}
			release, err := s.lock(path)
			if err != nil {
				return err
			}
			defer func() {
				if release != nil {
					if failure := release(); err == nil {
						err = failure
					}
				}
			}()
			current, err = s.Get(list, id)
			if err != nil || current == nil {
				return err
			}
			if len(derive) == 1 {
				patch, failure := derive[0](current.Clone())
				if failure != nil || patch == nil {
					return failure
				}
				input = patch.Clone()
			}
			previous := current.Clone()
			updates := protocol.NewJSONObject()
			if rawUpdate {
				updates = input.Clone()
			} else {
				for _, name := range []string{"subject", "description", "activeForm", "owner"} {
					if value := input.Get(name); value != nil && !jsonEqual(value, previous.Get(name)) {
						updates.Set(name, value)
					}
				}
				if value := input.Get("metadata"); value != nil {
					merged := protocol.NewJSONObject()
					if previous.Get("metadata") != nil {
						_ = merged.UnmarshalJSON(previous.Get("metadata"))
					}
					var patch protocol.JSONObject
					if err := patch.UnmarshalJSON(value); err != nil {
						return err
					}
					for _, property := range patch.OwnProperties() {
						if string(property.Value) == "null" {
							if err := merged.DeletePropertyJSON(property.NameJSON); err != nil {
								return err
							}
						} else {
							if err := merged.SetPropertyJSON(property.NameJSON, property.Value); err != nil {
								return err
							}
						}
					}
					updates.Set("metadata", raw(merged))
				}
				if value := input.Get("status"); value != nil && !jsonEqual(value, previous.Get("status")) {
					updates.Set("status", value)
				}
			}
			edgeInput := input
			if rawUpdate {
				edgeInput = protocol.NewJSONObject()
			}
			blocks, blockedBy := stringsFromJSON(previous.Get("blocks")), stringsFromJSON(previous.Get("blockedBy"))
			type related struct {
				id      json.RawMessage
				updates protocol.JSONObject
			}
			relatedUpdates := []related{}
			getRelated := func(target json.RawMessage) *protocol.JSONObject {
				for i := range relatedUpdates {
					if jsonEqual(relatedUpdates[i].id, target) {
						return &relatedUpdates[i].updates
					}
				}
				relatedUpdates = append(relatedUpdates, related{target, protocol.NewJSONObject()})
				return &relatedUpdates[len(relatedUpdates)-1].updates
			}
			for _, direction := range []struct {
				input      string
				next       *[]json.RawMessage
				reciprocal string
			}{{"addBlocks", &blocks, "blockedBy"}, {"addBlockedBy", &blockedBy, "blocks"}} {
				for _, target := range unique(stringsFromJSON(edgeInput.Get(direction.input))) {
					task := &previous
					if !jsonEqual(target, id) {
						task, err = s.Get(list, target)
						if err != nil {
							return err
						}
					}
					if task == nil {
						continue
					}
					if !contains(*direction.next, target) {
						*direction.next = append(*direction.next, target)
					}
					if jsonEqual(target, id) {
						if direction.reciprocal == "blocks" {
							if !contains(blocks, id) {
								blocks = append(blocks, id)
							}
						} else {
							if !contains(blockedBy, id) {
								blockedBy = append(blockedBy, id)
							}
						}
						continue
					}
					other := stringsFromJSON(task.Get(direction.reciprocal))
					if !contains(other, id) {
						other = append(other, id)
						getRelated(target).Set(direction.reciprocal, raw(other))
					}
				}
			}
			if !jsonEqual(raw(blocks), previous.Get("blocks")) {
				updates.Set("blocks", raw(blocks))
			}
			if !jsonEqual(raw(blockedBy), previous.Get("blockedBy")) {
				updates.Set("blockedBy", raw(blockedBy))
			}
			for _, property := range updates.OwnProperties() {
				var name string
				_ = json.Unmarshal(property.NameJSON, &name)
				if jsonEqual(property.Value, previous.Get(name)) {
					updates.Delete(name)
				}
			}
			targetChanged := len(updates.OwnProperties()) > 0
			changed := targetChanged || len(relatedUpdates) > 0
			var revision float64
			if changed {
				revision, err = s.bumpRevision(list)
			} else {
				revision, err = s.revision(list)
			}
			if err != nil {
				return err
			}
			updated := current
			if targetChanged {
				updated, err = s.updateUnsafe(list, id, updates)
				if err != nil || updated == nil {
					return err
				}
			}
			if err := release(); err != nil {
				return err
			}
			release = nil
			for _, entry := range relatedUpdates {
				actual, err := s.Get(list, entry.id)
				if err != nil {
					return err
				}
				if actual == nil {
					continue
				}
				patch := protocol.NewJSONObject()
				for _, name := range []string{"blocks", "blockedBy"} {
					if value := entry.updates.Get(name); value != nil && !jsonEqual(value, actual.Get(name)) {
						patch.Set(name, value)
					}
				}
				if len(patch.OwnProperties()) != 0 {
					if err := s.updateRelated(list, entry.id, patch); err != nil {
						return err
					}
				}
			}
			result = &Mutation{previous, *updated, changed, s.timestamp(), revision}
			return nil
		})
	})
	return result, err
}

func (s *Service) Delete(list string, id json.RawMessage) (result Deletion, err error) {
	err = s.withLifecycle(list, true, func(lifecycle) error {
		return s.withList(list, func() error {
			task, err := s.Get(list, id)
			if err != nil || task == nil {
				return err
			}
			revision, err := s.bumpRevision(list)
			if err != nil {
				return err
			}
			var idText string
			_ = json.Unmarshal(id, &idText)
			numeric := decimalPrefix(idText)
			if !math.IsNaN(numeric) && numeric > s.highWaterMark(list) {
				if err := os.WriteFile(filepath.Join(s.tasksDir(list), ".highwatermark"), []byte(numberText(numeric)), 0666); err != nil {
					return err
				}
			}
			path, err := s.taskPath(list, id)
			if err != nil {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			tasks, err := s.list(list)
			if err != nil {
				return err
			}
			for _, task := range tasks {
				updates := protocol.NewJSONObject()
				for _, name := range []string{"blocks", "blockedBy"} {
					existing := stringsFromJSON(task.Get(name))
					next := []json.RawMessage{}
					for _, value := range existing {
						if !jsonEqual(value, id) {
							next = append(next, value)
						}
					}
					if len(next) != len(existing) {
						updates.Set(name, raw(next))
					}
				}
				if len(updates.OwnProperties()) > 0 {
					if err := s.updateRelated(list, task.Get("id"), updates); err != nil {
						return err
					}
				}
			}
			s.notify()
			result = Deletion{true, s.timestamp(), &revision}
			return nil
		})
	})
	return result, err
}

func (s *Service) Snapshot(list string) (result Snapshot, err error) {
	err = s.withLifecycle(list, false, func(state lifecycle) error {
		if state.Deleted {
			for i := len(state.Terminals) - 1; i >= 0; i-- {
				terminal := state.Terminals[i]
				generation, _ := jsonNumber(terminal.Get("generation"))
				if generation == state.Generation && identityEqual(terminal.Get("identity"), state.Identity) {
					var tasks []json.RawMessage
					_ = json.Unmarshal(terminal.Get("tasks"), &tasks)
					parsed := []protocol.JSONObject{}
					for _, task := range tasks {
						parsed = append(parsed, *parseTask(task, false))
					}
					revision, _ := jsonNumber(terminal.Get("revision"))
					result = Snapshot{parsed, terminal.String("capturedAt"), revision}
					return nil
				}
			}
			return fmt.Errorf("Task list %s was deleted without a terminal frame", list)
		}
		return s.withList(list, func() error {
			tasks, err := s.list(list)
			if err != nil {
				return err
			}
			revision, err := s.revision(list)
			if err != nil {
				return err
			}
			result = Snapshot{tasks, s.timestamp(), revision}
			return nil
		})
	})
	return result, err
}
