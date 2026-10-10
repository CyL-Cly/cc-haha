package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type computerLock struct {
	SessionID  string          `json:"sessionId"`
	PID        float64         `json:"pid"`
	AcquiredAt json.RawMessage `json:"acquiredAt,omitempty"`
}
type lockState struct {
	Holder     string
	Held, Self bool
}

func (tools *NativeTools) lockPath() string {
	return filepath.Join(tools.options.Service.options.ConfigDir, "computer-use.lock")
}
func (tools *NativeTools) readLock() *computerLock {
	raw, err := os.ReadFile(tools.lockPath())
	if err != nil {
		return nil
	}
	var parsed computerLock
	value, ok := decodeObject(raw)
	if !ok {
		return nil
	}
	if _, ok := stringValue(value["sessionId"]); !ok {
		return nil
	}
	if _, err := optionalNumber(value["pid"], "pid"); err != nil || absent(value["pid"]) {
		return nil
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return nil
	}
	return &parsed
}
func (tools *NativeTools) checkLock() lockState {
	value := tools.readLock()
	if value == nil {
		return lockState{}
	}
	if value.SessionID == tools.sessionID() {
		return lockState{value.SessionID, true, true}
	}
	if processAlive(int(value.PID)) {
		return lockState{value.SessionID, true, false}
	}
	_ = os.Remove(tools.lockPath())
	return lockState{}
}
func (tools *NativeTools) acquireLock(ctx context.Context) (lockState, error) {
	if err := ctx.Err(); err != nil {
		return lockState{}, err
	}
	if err := os.MkdirAll(filepath.Dir(tools.lockPath()), 0777); err != nil {
		return lockState{}, err
	}
	create := func() (bool, error) {
		file, err := os.OpenFile(tools.lockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		raw := encode(map[string]any{"sessionId": tools.sessionID(), "pid": os.Getpid(), "acquiredAt": tools.now().UnixMilli()})
		_, writeErr := file.Write(raw)
		closeErr := file.Close()
		return writeErr == nil && closeErr == nil, errors.Join(writeErr, closeErr)
	}
	fresh := func() (lockState, error) {
		tools.lockHeld = true
		if ctx.Err() != nil {
			_ = tools.releaseLock(ctx)
			return lockState{}, errors.New("Computer Use cancelled during lock acquisition")
		}
		if tools.options.OnNotification != nil {
			tools.options.OnNotification(ctx, "computer_use_enter", "Claude is using your computer · press Ctrl+C to stop")
		}
		return lockState{tools.sessionID(), true, true}, nil
	}
	if created, err := create(); err != nil {
		return lockState{}, err
	} else if created {
		return fresh()
	}
	old := tools.readLock()
	if old != nil {
		if old.SessionID == tools.sessionID() {
			return lockState{old.SessionID, true, true}, nil
		}
		if processAlive(int(old.PID)) {
			return lockState{old.SessionID, true, false}, nil
		}
	}
	_ = os.Remove(tools.lockPath())
	if created, err := create(); err != nil {
		return lockState{}, err
	} else if created {
		return fresh()
	}
	if winner := tools.readLock(); winner != nil {
		return lockState{winner.SessionID, true, winner.SessionID == tools.sessionID()}, nil
	}
	return lockState{"unknown", true, false}, nil
}
func (tools *NativeTools) releaseLock(ctx context.Context) error {
	tools.lockHeld = false
	value := tools.readLock()
	if value == nil || value.SessionID != tools.sessionID() {
		return nil
	}
	err := os.Remove(tools.lockPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil && tools.options.OnNotification != nil {
		tools.options.OnNotification(ctx, "computer_use_exit", "Claude is done using your computer")
	}
	return err
}
