package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type timingTestState struct {
	trace            []string
	now              int64
	dateCalls        int
	serializeCalls   int
	dateEffects      map[int]bool
	serializeEffects map[int]error
	active           string
	process          *Process
}

func newTimingTest(t *testing.T, enabled bool) *timingTestState {
	t.Helper()
	state := &timingTestState{now: 1000, dateEffects: map[int]bool{}, serializeEffects: map[int]error{}}
	base := filepath.Join(t.TempDir(), "events")
	state.active = base + ".42.current.jsonl"
	process, err := NewProcess(Options{
		Environment: func(string) string {
			if enabled {
				return base
			}
			return ""
		},
		NowMillis: func() int64 {
			result := state.now
			state.now += 100
			state.trace = append(state.trace, fmt.Sprintf("now:%d", result))
			return result
		},
		Now: func() time.Time {
			state.dateCalls++
			state.trace = append(state.trace, "date")
			if state.dateEffects[state.dateCalls] {
				return time.UnixMilli(8640000000000001)
			}
			return time.UnixMilli(1700000000123)
		},
		Stringify: func(entry protocol.JSONObject) ([]byte, error) {
			state.serializeCalls++
			event := entry.Get("event")
			var name string
			if err := json.Unmarshal(event, &name); err != nil {
				return nil, err
			}
			state.trace = append(state.trace, name)
			if err := state.serializeEffects[state.serializeCalls]; err != nil {
				return nil, err
			}
			return StringifyEntry(entry)
		},
		PID: 42, Platform: "darwin", Files: OSFileSystem{},
	})
	if err != nil {
		t.Fatal(err)
	}
	state.process = process
	return state
}

func TestTimingEagerPrefixFreshPromiseAndActualReaction(t *testing.T) {
	state := newTimingTest(t, true)
	owner := cooperative.New()
	defer owner.Close()
	value := &struct{ Original bool }{true}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		original, _ := ownerasync.NewOwnerFuture[*struct{ Original bool }](owner)
		if err := original.Resolve(scope, value); err != nil {
			return err
		}
		returned, err := WithTiming(scope, owner, state.process, "event", func(next *cooperative.Scope) (TimingResult[*struct{ Original bool }], error) {
			if next != scope {
				return TimingResult[*struct{ Original bool }]{}, errors.New("fn changed caller scope")
			}
			state.trace = append(state.trace, "fn")
			return TimingFuture(original), nil
		}, nil)
		if err != nil {
			return err
		}
		if returned == original || returned.Identity() == original.Identity() {
			return errors.New("helper reused original promise")
		}
		want := []string{"now:1000", "date", "event_started", "fn"}
		if !reflect.DeepEqual(state.trace, want) {
			return fmt.Errorf("eager prefix=%v want=%v", state.trace, want)
		}
		state.trace = append(state.trace, "returned")
		observer, err := returned.Then(scope, func(_ *cooperative.Scope, got *struct{ Original bool }, failure error) error {
			if got != value || failure != nil {
				return errors.New("observer lost original value")
			}
			state.trace = append(state.trace, "observer")
			return nil
		})
		if err != nil {
			return err
		}
		if observer.Identity() == returned.Identity() {
			return errors.New("dependent observer reused helper")
		}
		got, err := returned.Await(scope)
		if err != nil {
			return err
		}
		if got != value {
			return errors.New("await copied value")
		}
		state.trace = append(state.trace, "await")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"now:1000", "date", "event_started", "fn", "returned", "now:1100", "date", "event_completed", "observer", "await"}
	if !reflect.DeepEqual(state.trace, want) {
		t.Fatalf("trace=%v want=%v", state.trace, want)
	}
}

func TestTimingImmediateValueGetDataClockSpreadAndOverrideOrder(t *testing.T) {
	state := newTimingTest(t, true)
	owner := cooperative.New()
	defer owner.Close()
	value := &struct{ Original bool }{true}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		returned, err := WithTiming(scope, owner, state.process, "event", func(*cooperative.Scope) (TimingResult[*struct{ Original bool }], error) {
			state.trace = append(state.trace, "fn")
			return TimingValue(value), nil
		}, func(_ *cooperative.Scope, got *struct{ Original bool }) (TimingData, error) {
			if got != value {
				return nil, errors.New("getData copied value")
			}
			state.trace = append(state.trace, "getData")
			return func(_ *cooperative.Scope, data *protocol.JSONObject) error {
				state.trace = append(state.trace, "getter")
				data.Set("z", json.RawMessage(`"first"`))
				data.Set("duration_ms", json.RawMessage("999"))
				data.Set("a", json.RawMessage(`"last"`))
				return nil
			}, nil
		})
		if err != nil {
			return err
		}
		state.trace = append(state.trace, "returned")
		got, err := returned.Await(scope)
		if err != nil {
			return err
		}
		if got != value {
			return errors.New("immediate value copied")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"now:1000", "date", "event_started", "fn", "returned", "getData", "now:1100", "getter", "date", "event_completed"}
	if !reflect.DeepEqual(state.trace, want) {
		t.Fatalf("trace=%v want=%v", state.trace, want)
	}
	raw := string(read(t, state.active))
	if !strings.Contains(raw, `"data":{"duration_ms":999,"z":"first","a":"last"}`) {
		t.Fatal(raw)
	}
}

func TestTimingSynchronousFnThrowLogsBeforeReturnAndKeepsIdentity(t *testing.T) {
	state := newTimingTest(t, true)
	owner := cooperative.New()
	defer owner.Close()
	failure := errors.New("original fn error")
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		returned, err := WithTiming(scope, owner, state.process, "event", func(*cooperative.Scope) (TimingResult[string], error) {
			state.trace = append(state.trace, "fn")
			return TimingResult[string]{}, failure
		}, nil)
		if err != nil {
			return err
		}
		want := []string{"now:1000", "date", "event_started", "fn", "now:1100", "date", "event_failed"}
		if !reflect.DeepEqual(state.trace, want) {
			return fmt.Errorf("sync throw boundary=%v", state.trace)
		}
		_, got := returned.Await(scope)
		if got != failure {
			return fmt.Errorf("error identity=%v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTimingStartedErrorBypassesFnAndFailedLog(t *testing.T) {
	for _, fault := range []string{"date", "serialization"} {
		t.Run(fault, func(t *testing.T) {
			state := newTimingTest(t, true)
			owner := cooperative.New()
			defer owner.Close()
			original := errors.New("started serialization")
			if fault == "date" {
				state.dateEffects[1] = true
			} else {
				state.serializeEffects[1] = original
			}
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				returned, err := WithTiming(scope, owner, state.process, "event", func(*cooperative.Scope) (TimingResult[string], error) {
					return TimingResult[string]{}, errors.New("fn must not run")
				}, nil)
				if err != nil {
					return err
				}
				_, failure := returned.Await(scope)
				if failure == nil {
					return errors.New("started failure fulfilled")
				}
				if fault == "serialization" && failure != original {
					return errors.New("started serializer identity replaced")
				}
				if fault == "date" {
					var date *InvalidDateError
					if !errors.As(failure, &date) || date.ErrorName() != "RangeError" || date.Error() != "Invalid Date" {
						return fmt.Errorf("date error=%v", failure)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if state.now != 1100 || state.dateCalls != 1 || strings.Contains(strings.Join(state.trace, ","), "event_failed") {
				t.Fatal(state.trace)
			}
			if _, err := os.Stat(state.active); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("started error wrote disk", err)
			}
		})
	}
}

func TestTimingErrorsAndFailedLogReplacement(t *testing.T) {
	for _, which := range []string{"fn-rejected", "getData", "getter", "completed-serializer", "failed-serializer", "completed-failed-serializer", "completed-failed-date"} {
		t.Run(which, func(t *testing.T) {
			state := newTimingTest(t, true)
			owner := cooperative.New()
			defer owner.Close()
			first, second := errors.New("original first"), errors.New("original failed log")
			want := first
			wantNow := int64(1200)
			switch which {
			case "getter", "completed-serializer":
				wantNow = 1300
			case "failed-serializer":
				state.serializeEffects[2] = second
				want = second
			case "completed-failed-serializer":
				state.serializeEffects[2] = first
				state.serializeEffects[3] = second
				want = second
				wantNow = 1300
			case "completed-failed-date":
				state.dateEffects[2] = true
				state.dateEffects[3] = true
				wantNow = 1300
			}
			if which == "completed-serializer" {
				state.serializeEffects[2] = first
			}
			var dateErrors []*InvalidDateError
			if which == "completed-failed-date" {
				state.process.options.Now = func() time.Time {
					state.dateCalls++
					if state.dateCalls > 1 {
						return time.UnixMilli(8640000000000001)
					}
					return time.UnixMilli(1700000000123)
				}
			}
			err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				returned, err := WithTiming(scope, owner, state.process, "event", func(next *cooperative.Scope) (TimingResult[string], error) {
					future, _ := ownerasync.NewOwnerFuture[string](owner)
					if which == "fn-rejected" || which == "failed-serializer" {
						if err := future.Reject(next, first); err != nil {
							return TimingResult[string]{}, err
						}
					} else {
						if err := future.Resolve(next, "original"); err != nil {
							return TimingResult[string]{}, err
						}
					}
					return TimingFuture(future), nil
				}, func(*cooperative.Scope, string) (TimingData, error) {
					if which == "getData" {
						return nil, first
					}
					if which == "getter" {
						return func(*cooperative.Scope, *protocol.JSONObject) error { return first }, nil
					}
					return nil, nil
				})
				if err != nil {
					return err
				}
				_, failure := returned.Await(scope)
				if which == "completed-failed-date" {
					var date *InvalidDateError
					if !errors.As(failure, &date) {
						return fmt.Errorf("replacement date=%v", failure)
					}
					dateErrors = append(dateErrors, date)
				} else if failure != want {
					return fmt.Errorf("identity got=%v want=%v", failure, want)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if state.now != wantNow {
				t.Fatalf("duration clocks=%d want=%d trace=%v", state.now, wantNow, state.trace)
			}
			if which == "completed-failed-date" && len(dateErrors) != 1 {
				t.Fatal(dateErrors)
			}
		})
	}
}

func TestTimingDisabledStillReadsDurationClockWithoutLogPrimitives(t *testing.T) {
	state := newTimingTest(t, false)
	owner := cooperative.New()
	defer owner.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		returned, err := WithTiming(scope, owner, state.process, "event", func(*cooperative.Scope) (TimingResult[string], error) { return TimingValue("original"), nil }, nil)
		if err != nil {
			return err
		}
		value, err := returned.Await(scope)
		if err != nil {
			return err
		}
		if value != "original" {
			return errors.New("disabled result")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.trace, []string{"now:1000", "now:1100"}) || state.dateCalls != 0 || state.serializeCalls != 0 {
		t.Fatal(state.trace)
	}
	if _, err := os.Stat(state.active); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestTimingObjectRetainsUndefinedAndUTF16OwnPropertyOrder(t *testing.T) {
	state := newTimingTest(t, true)
	owner := cooperative.New()
	defer owner.Close()
	var data protocol.JSONObject
	must(t, data.UnmarshalJSON([]byte(`{"z":1,"2":2,"a":"\ud800","duration_ms":999}`)))
	data.Set("z", nil)
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		returned, err := WithTiming(scope, owner, state.process, "event", func(*cooperative.Scope) (TimingResult[string], error) { return TimingValue("original"), nil }, func(*cooperative.Scope, string) (TimingData, error) { return TimingObject(data), nil })
		if err != nil {
			return err
		}
		_, err = returned.Await(scope)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(read(t, state.active))
	if !strings.Contains(raw, `"data":{"2":2,"duration_ms":999,"a":"\ud800"}`) {
		t.Fatal(raw)
	}
}

func TestTimingRejectsWrongOwnerBeforeClockAndKeepsAdmissionSeparate(t *testing.T) {
	state := newTimingTest(t, true)
	owner, other := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer other.Close()
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		returned, err := WithTiming(scope, other, state.process, "event", func(*cooperative.Scope) (TimingResult[string], error) { return TimingValue("original"), nil }, nil)
		if returned != nil || !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("guard returned=%v error=%v", returned, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.trace) != 0 {
		t.Fatal(state.trace)
	}
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		returned, err := WithTiming(scope, owner, state.process, "event", func(*cooperative.Scope) (TimingResult[string], error) { return TimingFuture[string](nil), nil }, nil)
		if returned != nil || !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("nil future admission returned=%v error=%v", returned, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.now != 1100 || strings.Contains(strings.Join(state.trace, ","), "event_failed") {
		t.Fatal(state.trace)
	}
}

func TestTimingPendingAndObserverOnlyKeepCallerBodyBeforeSettlement(t *testing.T) {
	state := newTimingTest(t, true)
	owner := cooperative.New()
	defer owner.Close()
	value := &struct{ Original bool }{true}
	var observation *ownerasync.OwnerFuture[struct{}]
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		original, _ := ownerasync.NewOwnerFuture[*struct{ Original bool }](owner)
		returned, err := WithTiming(scope, owner, state.process, "event", func(*cooperative.Scope) (TimingResult[*struct{ Original bool }], error) {
			return TimingFuture(original), nil
		}, nil)
		if err != nil {
			return err
		}
		observation, err = returned.Then(scope, func(_ *cooperative.Scope, got *struct{ Original bool }, failure error) error {
			if got != value || failure != nil {
				return errors.New("observer lost pending value")
			}
			state.trace = append(state.trace, "observer")
			return nil
		})
		if err != nil {
			return err
		}
		if state.now != 1100 {
			return errors.New("pending promise consumed completion clock")
		}
		state.trace = append(state.trace, "bodyEnd")
		// This is a declared independent settlement input, not a native timing
		// claim. Neither the caller nor helper await the observer or fn here.
		_, err = owner.Microtask(cooperative.Read, func(next *cooperative.Scope) error {
			state.trace = append(state.trace, "independentSettlement")
			return original.Resolve(next, value)
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// Administrative capture of the observer already registered by the caller
	// adds no Then/Owner job and does not prove Bun process natural-exit policy.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := observation.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"now:1000", "date", "event_started", "bodyEnd", "independentSettlement", "now:1100", "date", "event_completed", "observer"}
	if !reflect.DeepEqual(state.trace, want) {
		t.Fatalf("pending trace=%v want=%v", state.trace, want)
	}
}

func TestTimingOwnerCloseDoesNotRunCompletionOrPromoteAdministrativeFailure(t *testing.T) {
	state := newTimingTest(t, true)
	owner := cooperative.New()
	var returned *ownerasync.OwnerFuture[string]
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		original, _ := ownerasync.NewOwnerFuture[string](owner)
		var err error
		returned, err = WithTiming(scope, owner, state.process, "event", func(*cooperative.Scope) (TimingResult[string], error) { return TimingFuture(original), nil }, nil)
		return err
	})
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	owner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := returned.Wait(ctx); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal(err)
	}
	if state.now != 1100 || !reflect.DeepEqual(state.trace, []string{"now:1000", "date", "event_started"}) {
		t.Fatal(state.trace)
	}
}

func TestTimingInvalidDatesCreateDistinctRangeErrorObjects(t *testing.T) {
	_, first := isoDate(time.UnixMilli(8640000000000001))
	_, second := isoDate(time.UnixMilli(8640000000000001))
	if first == nil || second == nil || first == second {
		t.Fatal("invalid date error identity reused")
	}
	var date *InvalidDateError
	if !errors.As(second, &date) || date.ErrorName() != "RangeError" || date.Error() != "Invalid Date" {
		t.Fatal(second)
	}
}
