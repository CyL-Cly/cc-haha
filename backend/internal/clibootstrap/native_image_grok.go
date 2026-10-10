package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/grokauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type nativeGrokScopeKey struct{}

func (m *MainPrint) nativeGrokImageScope(ctx context.Context) (*cooperative.Scope, error) {
	scope, _ := ctx.Value(nativeGrokScopeKey{}).(*cooperative.Scope)
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	return scope, nil
}
func (m *MainPrint) nativeGrokImageTokens(ctx context.Context, scope *cooperative.Scope, force bool) (*protocol.JSONObject, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if m.grokImages == nil {
		service, err := grokauth.New(grokauth.Options{Environment: m.nativeOpenAIEnvironment, NowMillis: func() int64 { return int64(m.options.Native.NowMillis()) }, Administrative: func(err error) bool {
			return errors.Is(err, cooperative.ErrRestricted) || errors.Is(err, cooperative.ErrClosed) || errors.Is(err, cooperative.ErrStop) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
		},
			Read: func(ctx context.Context, path string) ([]byte, error) {
				scope, err := m.nativeGrokImageScope(ctx)
				if err != nil {
					return nil, err
				}
				if !filepath.IsAbs(path) {
					path = filepath.Join(m.workingDirectory(), path)
				}
				future, err := nativeSkillWork(m, scope, func() ([]byte, error) { return m.options.Native.readFile(path) })
				if err != nil {
					return nil, err
				}
				return future.Await(scope)
			},
			Save: func(ctx context.Context, path string, tokens protocol.JSONObject) (bool, error) {
				scope, err := m.nativeGrokImageScope(ctx)
				if err != nil {
					return false, err
				}
				if !filepath.IsAbs(path) {
					path = filepath.Join(m.workingDirectory(), path)
				}
				if err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil }); err != nil {
					return false, err
				}
				files := mainSettingsFiles{n: m.options.Native}
				temporary := path + ".tmp." + strconv.Itoa(m.options.PID) + "." + strconv.FormatInt(int64(m.options.Native.NowMillis()), 10)
				renamed := false
				defer func() {
					if !renamed {
						_ = files.Remove(temporary)
					}
				}()
				if err = files.MkdirAll(filepath.Dir(path)); err != nil {
					return false, nil
				}
				data, _ := json.MarshalIndent(tokens, "", "  ")
				data = append(data, '\n')
				if err = func() error {
					if err := m.options.Native.check("writeFile", temporary); err != nil {
						return err
					}
					return os.WriteFile(temporary, data, 0600)
				}(); err != nil {
					return false, nil
				}
				if err = files.Rename(temporary, path); err != nil {
					return false, nil
				}
				renamed = true
				return true, nil
			},
			Refresh: func(ctx context.Context, token, client json.RawMessage) (protocol.JSONObject, error) {
				scope, err := m.nativeGrokImageScope(ctx)
				if err != nil {
					return protocol.JSONObject{}, err
				}
				plan, err := desktopoauth.GrokRefreshRequest(token, client)
				if err != nil {
					return protocol.JSONObject{}, err
				}
				headers := http.Header{}
				for _, header := range plan.Headers {
					headers.Set(header[0], header[1])
				}
				response, err := m.nativeImageFetch(context.WithoutCancel(ctx), scope, imagegeneration.Request{URL: plan.URL, Method: plan.Method, Headers: headers, Body: []byte(plan.Body)})
				if err != nil {
					return protocol.JSONObject{}, err
				}
				return desktopoauth.DecodeGrokRefreshResponse(response.Status, response.Body, response.ReadError)
			},
		})
		if err != nil {
			return nil, err
		}
		m.grokImages = service
	}
	return m.grokImages.Fresh(context.WithValue(ctx, nativeGrokScopeKey{}, scope), force)
}
