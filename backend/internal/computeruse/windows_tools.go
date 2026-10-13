package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"sync"
	"time"
)

// WindowsTools is the original pixel-tool face. It shares consent, the actual
// SID, native transport and global lock with the desktop service. The macOS
// accessibility/JavaScript face remains a separate platform implementation.
type WindowsToolsOptions struct {
	Native         NativeToolsOptions
	CoordinateMode string
	SubGates       func() WindowsSubGates
	Sleep          func(context.Context, time.Duration) error
	CropRawPatch   func(string, WindowsCropRect) ([]byte, error)
}
type WindowsSubGates struct {
	PixelValidation, ClipboardPasteMultiline, MouseAnimation bool
	HideBeforeAction, AutoTargetDisplay, ClipboardGuard      bool
}
type WindowsTools struct {
	authority *NativeTools
	options   WindowsToolsOptions
	mu        sync.Mutex
	closed    bool
	workers   sync.WaitGroup
	last      *windowsScreenshot
	selected  *float64
	pinned    bool
	resolved  string
	held      bool
}
type windowsScreenshot struct {
	Base64                                 string `json:"base64"`
	Width, Height                          float64
	DisplayWidth, DisplayHeight, DisplayID float64
	OriginX, OriginY                       float64
}

func NewWindowsTools(options WindowsToolsOptions) (*WindowsTools, error) {
	if options.Native.Service == nil || options.Native.Service.options.Platform != "win32" {
		return nil, errors.New("Windows Computer Use requires the shared Windows native transport")
	}
	if options.CoordinateMode == "" {
		options.CoordinateMode = "pixels"
	}
	if options.CoordinateMode != "pixels" && options.CoordinateMode != "normalized_0_100" {
		return nil, errors.New("unsupported Windows Computer Use coordinate mode")
	}
	if options.SubGates == nil {
		options.SubGates = func() WindowsSubGates {
			return WindowsSubGates{ClipboardPasteMultiline: true, MouseAnimation: true, HideBeforeAction: true, AutoTargetDisplay: true, ClipboardGuard: true}
		}
	}
	if options.Sleep == nil {
		options.Sleep = func(ctx context.Context, duration time.Duration) error {
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
	}
	authority, err := NewNativeTools(options.Native)
	if err != nil {
		return nil, err
	}
	return &WindowsTools{authority: authority, options: options}, nil
}
func (p *WindowsTools) CoordinateMode() string            { return p.options.CoordinateMode }
func (p *WindowsTools) Disabled(ctx context.Context) bool { return p.authority.Disabled(ctx) }

func (p *WindowsTools) native(ctx context.Context, command string, payload any) (json.RawMessage, error) {
	return p.authority.options.NativeCall(ctx, command, encode(payload))
}
func pixelError(message, kind string) NativeToolResult {
	result := textResult(message)
	result.IsError = true
	if kind != "" {
		result.Telemetry = map[string]string{"error_kind": kind}
	}
	return result
}
func (p *WindowsTools) Call(ctx context.Context, name string, raw json.RawMessage) (NativeToolResult, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return cancelledResult("Computer Use cancelled before dispatch."), nil
	}
	p.workers.Add(1)
	p.mu.Unlock()
	defer p.workers.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.authority.ctx, cancel)
	defer func() { stop(); cancel() }()
	previous, release := p.authority.enqueue()
	defer release()
	if previous != nil {
		select {
		case <-previous:
		default:
			select {
			case <-previous:
			case <-ctx.Done():
				return cancelledResult("Computer Use cancelled before dispatch."), nil
			}
		}
	}
	if ctx.Err() != nil {
		result := cancelledResult("Computer Use cancelled before dispatch.")
		if cleanup, err := p.releaseHeld(context.WithoutCancel(ctx)); err != nil {
			result.Content[0].Text += " Could not release this session's held mouse: " + err.Error()
		} else if !cleanup && p.held {
			result.Content[0].Text += " Held mouse cleanup was skipped because this session no longer owns the Computer Use lock."
		}
		return result, nil
	}
	lock := p.authority.checkLock()
	if lock.Held && !lock.Self {
		return toolError(p.authority.options.FormatLockHeld(lock.Holder), "cu_lock_held"), nil
	}
	if !lock.Held && name != "request_access" && name != "list_granted_applications" {
		var err error
		lock, err = p.authority.acquireLock(ctx)
		if err != nil {
			return NativeToolResult{}, err
		}
		if lock.Held && !lock.Self {
			return toolError(p.authority.options.FormatLockHeld(lock.Holder), "cu_lock_held"), nil
		}
		p.held = false
	}
	if p.Disabled(ctx) {
		return pixelError("Computer control is disabled in Settings. Enable it and try again.", "other"), nil
	}
	args, _ := decodeObject(raw)
	if args == nil {
		args = object{}
	}
	result, err := p.dispatch(ctx, name, args, p.options.SubGates())
	if err != nil {
		result = pixelRawError("executor_threw", pixelQuoted(fmt.Sprintf("Tool \"%s\" failed: ", name)), nativeErrorMessageJSON(err))
	}
	if ctx.Err() != nil {
		message := "Computer Use cancelled after dispatch. Inspect the current state before continuing."
		messageJSON := pixelQuoted(message)
		if cleanup, err := p.releaseHeld(context.WithoutCancel(ctx)); err != nil {
			messageJSON, _ = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{messageJSON, pixelQuoted(" Could not release this session's held mouse: "), nativeErrorMessageJSON(err)}, "")
		} else if !cleanup && p.held {
			messageJSON, _ = protocol.JoinJavaScriptValuesJSON([]json.RawMessage{messageJSON, pixelQuoted(" Held mouse cleanup was skipped because this session no longer owns the Computer Use lock.")}, "")
		}
		result.Content = append(result.Content, pixelRawMessage(messageJSON).Content...)
		result.IsError = true
	}
	return result, nil
}
func (p *WindowsTools) releaseHeld(ctx context.Context) (bool, error) {
	if !p.held {
		return true, nil
	}
	lock := p.authority.checkLock()
	if !lock.Self {
		return false, nil
	}
	if _, err := p.native(ctx, "mouse_up", object{}); err != nil {
		return false, err
	}
	p.held = false
	return true, nil
}
func (p *WindowsTools) EndTurn(ctx context.Context) error {
	previous, release := p.authority.enqueue()
	defer release()
	if previous != nil {
		select {
		case <-previous:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	_, mouseErr := p.releaseHeld(ctx)
	return errors.Join(mouseErr, p.authority.endTurnUnqueued(ctx))
}
func (p *WindowsTools) Close(ctx context.Context) error {
	p.mu.Lock()
	p.closed = true
	p.authority.cancel()
	p.mu.Unlock()
	p.workers.Wait()
	return errors.Join(p.EndTurn(ctx), p.authority.options.Service.CloseSession(ctx, p.authority.sessionID()))
}
