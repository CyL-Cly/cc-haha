package clibootstrap

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/imagegeneration"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/providernative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
)

// Each call captures its current Scope. Only immutable physical file/HTTP
// operations leave it; authorization, token cache and image parsing remain on
// the same canonical Harness owner.
func (m *MainPrint) nativeImageBindings() *builtintools.ImageBindings {
	return &builtintools.ImageBindings{
		Read: m.startNativeImageRead, MetadataMessage: m.nativeImageMetadataMessage,
		Config: func(scope *cooperative.Scope) (*imagegeneration.Config, error) {
			if !scope.BelongsTo(m.Owner) {
				return nil, cooperative.ErrRestricted
			}
			return imagegeneration.RuntimeConfig(m.nativeOpenAIEnvironment()), nil
		},
		Generate: func(ctx context.Context, scope *cooperative.Scope, input protocol.JSONObject, config imagegeneration.Config) (imagegeneration.Output, error) {
			var output imagegeneration.Output
			operation := "image_generate"
			if input.Get("referenced_image_paths") != nil {
				operation = "image_edit"
			}
			err := m.nativeImageHTTP.Run(scope, ctx, operation, func(next *cooperative.Scope) error {
				var err error
				output, err = m.generateNativeImage(ctx, next, input, config)
				return err
			})
			return output, err
		},
		RegisterUserImage: func(ctx context.Context, scope *cooperative.Scope, path string) error {
			if !scope.BelongsTo(m.Owner) {
				return cooperative.ErrRestricted
			}
			resolved, err := (nativeOwnedImageFiles{m, scope, imagegeneration.NativeFiles{Check: m.options.Native.check}}).Realpath(ctx, path)
			if err != nil {
				if errors.Is(err, cooperative.ErrRestricted) || errors.Is(err, cooperative.ErrClosed) || errors.Is(err, cooperative.ErrStop) {
					return err
				}
				return nil
			}
			return m.nativeRememberUserImage(scope, resolved)
		},
	}
}

func (m *MainPrint) generateNativeImage(ctx context.Context, scope *cooperative.Scope, input protocol.JSONObject, config imagegeneration.Config) (imagegeneration.Output, error) {
	if !scope.BelongsTo(m.Owner) {
		return imagegeneration.Output{}, cooperative.ErrRestricted
	}
	environment := m.nativeOpenAIEnvironment()
	directory := environment["CLAUDE_CONFIG_DIR"]
	if directory == "" {
		directory = filepath.Join(m.options.Native.Home, ".claude")
	}
	service, err := imagegeneration.New(imagegeneration.Options{
		CombinedSignal: func(parent context.Context, delay time.Duration) (context.Context, func() error, error) {
			return m.nativeCombinedImageSignal(scope, parent, delay)
		},
		IsAdministrativeError: func(err error) bool {
			return provider.IsOwnerWaitFailure(err) || errors.Is(err, cooperative.ErrRestricted) || errors.Is(err, cooperative.ErrClosed) || errors.Is(err, cooperative.ErrStop) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
		},
		Files: nativeOwnedImageFiles{m, scope, imagegeneration.NativeFiles{Check: m.options.Native.check}},
		Fetch: func(ctx context.Context, input imagegeneration.Request) (imagegeneration.Response, error) {
			return m.nativeImageFetch(ctx, scope, input)
		},
		Environment: func() map[string]string { return environment }, ConfigHome: directory,
		SessionID: m.Controller.SessionID, NowMillis: func() int64 { return int64(m.options.Native.NowMillis()) }, UUID: uuidMain,
		UserProvidedCheck: func(_ context.Context, path string) (bool, error) {
			return m.nativeUserImageAuthorized(scope, path)
		},
		PrepareInputs: func(ctx context.Context, paths, roots []string) ([]imagegeneration.PreparedImage, error) {
			return m.prepareNativeImageInputs(ctx, scope, paths, roots, imagegeneration.NativeFiles{Check: m.options.Native.check})
		},
		OpenAITokens: func(ctx context.Context) (*protocol.JSONObject, error) {
			if m.openAIAuth == nil {
				return nil, mainCap("shared OpenAI OAuth storage")
			}
			value, err := m.openAIAuth.EnsureFresh(ctx, scope)
			if value == nil || err != nil {
				return nil, err
			}
			object := value.Object
			return &object, nil
		},
		GrokTokens: func(ctx context.Context, force bool) (*protocol.JSONObject, error) {
			return m.nativeGrokImageTokens(ctx, scope, force)
		},
		Download: func(ctx context.Context, url string) ([]byte, error) {
			future, err := nativeSkillWork(m, scope, func() ([]byte, error) {
				return imagegeneration.DownloadPublic(ctx, url, imagegeneration.PublicDownloadOptions{})
			})
			if err != nil {
				return nil, err
			}
			return future.Await(scope)
		},
	})
	if err != nil {
		return imagegeneration.Output{}, err
	}
	return service.Generate(ctx, input, config)
}

func (m *MainPrint) nativeImageFetch(ctx context.Context, scope *cooperative.Scope, input imagegeneration.Request) (imagegeneration.Response, error) {
	if !scope.BelongsTo(m.Owner) {
		return imagegeneration.Response{}, cooperative.ErrRestricted
	}
	environment := m.nativeOpenAIEnvironment()
	if err := tlsconfig.ValidateOriginalTLSOptions(environment); err != nil {
		return imagegeneration.Response{}, err
	}
	certificates, err := m.tls.Certificates(environment)
	if err != nil {
		return imagegeneration.Response{}, err
	}
	exchange, err := provider.NativeDirectHTTPExchangeFromEnvironment(input.URL, environment)
	if err != nil {
		return imagegeneration.Response{}, err
	}
	parsedURL, err := url.Parse(input.URL)
	if err != nil {
		return imagegeneration.Response{}, err
	}
	if parsedURL.Scheme == "https" {
		exchange.ResolveVerifiedTLS = func() (*tls.Config, error) { return tlsconfig.VerifiedConfigFromCertificates(certificates) }
	}
	driver, err := providernative.New(m.Owner, scope, nil)
	if err != nil {
		return imagegeneration.Response{}, err
	}
	bodyIO, err := provider.NewOwnerBodyIO(driver, func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
		return scope.AwaitBytes(cooperative.StreamNext, work)
	})
	if err != nil {
		return imagegeneration.Response{}, err
	}
	request, err := http.NewRequestWithContext(ctx, input.Method, input.URL, bytes.NewReader(input.Body))
	if err != nil {
		return imagegeneration.Response{}, err
	}
	request.Header = input.Headers.Clone()
	mode := "follow"
	if input.RedirectError {
		mode = "error"
	}
	response, err := driver.OpenFetchWithOptions(request, exchange, provider.NativeFetchOptions{Redirect: mode, ResolveExchange: func(target *url.URL) (*provider.RawHTTP1Exchange, error) {
		selected := *exchange
		if target.Scheme == "https" {
			selected.ResolveVerifiedTLS = func() (*tls.Config, error) { return tlsconfig.VerifiedConfigFromCertificates(certificates) }
		} else {
			selected.VerifiedTLS, selected.ResolveVerifiedTLS = nil, nil
		}
		return &selected, nil
	}})
	if err != nil {
		return imagegeneration.Response{}, err
	}
	defer response.Response.Body.Close()
	data, readErr := bodyIO.ReadFetchBody(response.Response.Body)
	if provider.IsOwnerWaitFailure(readErr) {
		return imagegeneration.Response{}, readErr
	}
	if readErr != nil && ctx.Err() != nil {
		readErr = provider.NewNativeFetchAbortError(context.Cause(ctx))
	}
	return imagegeneration.Response{Status: response.Response.StatusCode, Headers: response.Response.Header.Clone(), Body: data, ReadError: readErr}, nil
}

type nativeOwnedImageFiles struct {
	main  *MainPrint
	scope *cooperative.Scope
	files imagegeneration.NativeFiles
}

func imagePhysical[T any](f nativeOwnedImageFiles, work func() (T, error)) (T, error) {
	var empty T
	future, err := nativeSkillWork(f.main, f.scope, work)
	if err != nil {
		return empty, err
	}
	return future.Await(f.scope)
}
func (f nativeOwnedImageFiles) Realpath(ctx context.Context, path string) (string, error) {
	return imagePhysical(f, func() (string, error) { return f.files.Realpath(ctx, path) })
}
func (f nativeOwnedImageFiles) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	return imagePhysical(f, func() (fs.FileInfo, error) { return f.files.Stat(ctx, path) })
}
func (f nativeOwnedImageFiles) ReadNoFollow(ctx context.Context, path string) ([]byte, error) {
	return imagePhysical(f, func() ([]byte, error) { return f.files.ReadNoFollow(ctx, path) })
}
func (f nativeOwnedImageFiles) MkdirAll(ctx context.Context, path string, mode fs.FileMode) error {
	_, err := imagePhysical(f, func() (struct{}, error) { return struct{}{}, f.files.MkdirAll(ctx, path, mode) })
	return err
}
func (f nativeOwnedImageFiles) Chmod(ctx context.Context, path string, mode fs.FileMode) error {
	_, err := imagePhysical(f, func() (struct{}, error) { return struct{}{}, f.files.Chmod(ctx, path, mode) })
	return err
}
func (f nativeOwnedImageFiles) WriteExclusive(ctx context.Context, path string, data []byte, mode fs.FileMode) error {
	immutable := append([]byte(nil), data...)
	_, err := imagePhysical(f, func() (struct{}, error) { return struct{}{}, f.files.WriteExclusive(ctx, path, immutable, mode) })
	return err
}
