package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/autoquestion"
	"github.com/NanmiCoder/cc-haha/backend/internal/conversationapi"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/access"
	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/capabilitymentions"
	"github.com/NanmiCoder/cc-haha/backend/internal/clilauncher"
	"github.com/NanmiCoder/cc-haha/backend/internal/computeruse"
	"github.com/NanmiCoder/cc-haha/backend/internal/connectors"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/doctorapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/h5access"
	"github.com/NanmiCoder/cc-haha/backend/internal/httpapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/imregistration"
	"github.com/NanmiCoder/cc-haha/backend/internal/localindex"
	"github.com/NanmiCoder/cc-haha/backend/internal/marketapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/memoryapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativeprovidermodels"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativeprovidertest"
	"github.com/NanmiCoder/cc-haha/backend/internal/opentargets"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/NanmiCoder/cc-haha/backend/internal/publicaccess"
	"github.com/NanmiCoder/cc-haha/backend/internal/scheduler"
	"github.com/NanmiCoder/cc-haha/backend/internal/searchapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerproxytransport"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncollaboration"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/sidequestion"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/slashcommandsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/systemprompt"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
	"github.com/NanmiCoder/cc-haha/backend/internal/uipreferences"
	"github.com/NanmiCoder/cc-haha/backend/internal/voice"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

type Options struct {
	Market                                          *marketapi.Service
	InspectionModelOptions                          *modelaccounting.NativeCalculatorOptions
	InspectionContextWindow                         func(context.Context, string, protocol.JSONObject) (float64, error)
	AutoQuestion                                    *autoquestion.Service
	Voice                                           *voice.Service
	CLILauncher                                     *clilauncher.Service
	HomeDir, ConfigDir, AppRoot, WorkDir, H5DistDir string
	Version, TrustedRendererOrigin                  string
	AuthRequired                                    bool
	Environment                                     func() map[string]string
	Authorization                                   access.Authorization
	Factory                                         SessionFactory
	Scheduler                                       *scheduler.Service
	Teams                                           *teamsapi.Service
	Store                                           *storage.Store
	HistoryBudget                                   *sessionstore.Budget
	Search                                          *searchapi.Service
	LocalIndex                                      *localindex.Coordinator
	Settings                                        *settings.Service
	Managed                                         *settings.ManagedService
	Preferences                                     *uipreferences.Service
	H5                                              *h5access.Service
	OutputStyles                                    *systemprompt.StyleLoader
	Skills                                          *skillregistry.Service
	SkillMentions                                   func(context.Context, string, string) (capabilitymentions.Response, error)
	SkillSlashCommands                              func(context.Context, string, bool) ([]slashcommandsapi.Command, error)
	Memory                                          *memoryapi.Service
	OpenTargets                                     *opentargets.Service
	Connectors                                      func(context.Context) (*connectors.Service, error)
	MCP                                             func(context.Context, string) (*mcpapi.Service, error)
	Diagnostics                                     *diagnosticsapi.Service
	Doctor                                          *doctorapi.Service
	ComputerUse                                     *computeruse.Service
	HahaOAuth                                       *desktopoauth.Service
	Adapters                                        *adapterconfig.Service
	IMRegistration                                  *imregistration.Service
	FeishuRegistration                              *imregistration.FeishuService
	WecomRegistration                               *imregistration.WecomService
	QQRegistration                                  *imregistration.QQService
	WhatsAppRegistration                            *imregistration.WhatsAppService
	WechatRegistration                              *imregistration.WechatService
	HahaOpenAIOAuth                                 *desktopoauth.OpenAIService
	HahaGrokOAuth                                   *desktopoauth.GrokService
	GrokModelCatalog                                func(context.Context) ([]providerproxy.GrokModel, error)
	GrokRuntimeModelCatalog                         func() []providerproxy.GrokModel
	OpenAIModelCatalog                              func(context.Context) ([]providerproxy.OpenAIModel, error)
	ProviderModels                                  *nativeprovidermodels.Service
	ProviderTest                                    *nativeprovidertest.Service
	ProviderProxy                                   *providerproxytransport.Service
	ProviderProxyPort                               int
	ActivityStats                                   *activitystatsapi.Service
	Trace                                           *tracecapture.Service
	Agents                                          *agentsapi.Service
	Plugins                                         func(context.Context, string) (*pluginsapi.Service, error)
	RespectGitignore                                func(context.Context) (bool, error)
	RecordedCommandIsReadOnly                       func(string) bool
	ClassifyRecordedCommand                         func(string) (bool, error)
	OnShutdown                                      func(context.Context) error
	WorkflowAgentRunState                           func(context.Context, string, string) (json.RawMessage, error)
	Workflows                                       *workflow.Service
	PersistSession                                  func(context.Context) (bool, error)
	OnUnhandled                                     func(httpapi.UnhandledError)
}

type runtimeSession struct {
	slashCommands       []protocol.JSONObject
	teamWorkerOrdinal   uint64
	mu                  sync.Mutex
	ctx                 context.Context
	cancel              context.CancelFunc
	options             SessionOptions
	live                LiveSession
	opening             chan struct{}
	closing             chan struct{}
	retired             bool
	closeError          error
	err                 error
	revokeCollaboration func()
	timer               *time.Timer
	epoch               uint64
	admission           []admissionFrame
	wake                chan struct{}
	admitted            chan struct{}
	admitting           bool
}

type Server struct {
	publicAccess            *publicaccess.Server
	inspectionSnapshots     map[string]*inspectionPending
	inspectionSnapshotOrder []string
	historyRecoveryCache    map[string]historyRecoveryCacheEntry
	historyRecoveryOrder    []string
	teamWorkerSequence      uint64
	teams                   *teamRuntimeState
	activePet               *connection
	sdk                     *sdkState
	conversations           *conversationapi.Service
	sideQuestions           *sidequestion.Service
	options                 Options
	ctx                     context.Context
	cancel                  context.CancelFunc
	mu                      sync.Mutex
	runtimes                map[string]*runtimeSession
	deleting                map[string]bool
	connections             map[string]map[*connection]struct{}
	closed                  bool
	closeDone               chan struct{}
	closeError              error
	apiOperations           sync.WaitGroup
	reader                  *sessionstore.Reader
	metadata                *sessionmetadata.Reader
	projection              *sessionprojection.Service
	budget                  *sessionstore.Budget
	desktopUI               *httpapi.DesktopUI
	h5                      *httpapi.H5Access
	grace                   *httpapi.H5GraceCache
	startedAt               time.Time
	recentProjects          *recentProjectCache
	projectHistory          *projectSessionHistory
	sideChats               map[string]*sideChat
	sessionMutation         uint64
	filesystemRoots         map[string]bool
	filesystemSearches      map[string]*filesystemSearch
	gitRoots                *gitRootCache
	checkpoints             *sessionrewind.Service
	gitCanonicalRoots       *gitRootCache
	subagentLookups         map[string]subagentLookupCache
	subagentLookupKeys      []string
	workspaceWatches        map[chan struct{}]struct{}
	memoryLaunch            map[string]sessionInfo
	knownSessions           map[string]bool
	privateTitles           map[string]map[string]bool
	collaboration           *collaborationHost
}

func New(options Options) (*Server, error) {
	if !filepath.IsAbs(options.HomeDir) || !filepath.IsAbs(options.ConfigDir) || options.Factory == nil {
		return nil, errors.New("Go server requires explicit absolute home/config paths and the shared application session factory")
	}
	if options.WorkDir == "" {
		options.WorkDir = options.HomeDir
	}
	if !filepath.IsAbs(options.WorkDir) {
		return nil, errors.New("Go server working directory must be absolute")
	}
	if options.Store == nil {
		options.Store = storage.New(storage.Options{})
	}
	if options.GrokRuntimeModelCatalog == nil && options.HahaGrokOAuth != nil {
		options.GrokRuntimeModelCatalog = options.HahaGrokOAuth.RuntimeModelCatalog
	}
	if options.GrokModelCatalog == nil && options.HahaGrokOAuth != nil {
		options.GrokModelCatalog = func(ctx context.Context) ([]providerproxy.GrokModel, error) {
			return options.HahaGrokOAuth.ModelCatalog(ctx, false)
		}
	}
	if options.OpenAIModelCatalog == nil && options.HahaOpenAIOAuth != nil {
		options.OpenAIModelCatalog = func(ctx context.Context) ([]providerproxy.OpenAIModel, error) {
			return options.HahaOpenAIOAuth.ModelCatalog(ctx, false)
		}
	}
	if options.OnUnhandled == nil {
		options.OnUnhandled = func(event httpapi.UnhandledError) { fmt.Fprintln(os.Stderr, event.Error) }
	}
	if options.H5 != nil && options.Authorization.ValidateH5Token == nil {
		options.Authorization.ValidateH5Token = options.H5.ValidateToken
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{options: options, ctx: ctx, cancel: cancel, closeDone: make(chan struct{}), runtimes: make(map[string]*runtimeSession), deleting: make(map[string]bool), connections: make(map[string]map[*connection]struct{}), reader: sessionstore.NewReader(nil), metadata: sessionmetadata.NewReader(nil), startedAt: time.Now(), memoryLaunch: make(map[string]sessionInfo), knownSessions: make(map[string]bool), privateTitles: make(map[string]map[string]bool)}
	var err error
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	server.budget = options.HistoryBudget
	if server.budget == nil {
		server.budget = sessionstore.NewBudget()
	}
	server.reader = sessionstore.NewReaderWithBudget(nil, server.budget)
	server.projection, err = sessionprojection.New(sessionprojection.Options{Reader: server.metadata, Budget: server.budget, Stat: projectionStat, ConfigDir: func() string { return options.ConfigDir }, Cwd: func() string { return options.WorkDir }, Platform: platform, ParseDate: sessionprojection.ParseDateUTC})
	if err != nil {
		cancel()
		return nil, err
	}
	if options.Preferences != nil {
		server.desktopUI, err = httpapi.NewDesktopUI(httpapi.DesktopUIOptions{Service: options.Preferences, Authorization: func() access.Authorization { return options.Authorization }, OnUnhandled: options.OnUnhandled})
		if err != nil {
			cancel()
			return nil, err
		}
	}
	if options.H5 != nil {
		grace, graceErr := httpapi.NewH5GraceCache(options.H5)
		if graceErr != nil {
			cancel()
			return nil, graceErr
		}
		server.grace = grace
		grace.Refresh(ctx)
		server.h5, err = httpapi.NewH5Access(httpapi.H5AccessOptions{Service: options.H5, GraceCache: grace, OnUnhandled: options.OnUnhandled})
		if err != nil {
			cancel()
			return nil, err
		}
	}
	if options.RecordedCommandIsReadOnly != nil || options.ClassifyRecordedCommand != nil {
		server.checkpoints, err = server.newCheckpointService()
		if err != nil {
			cancel()
			return nil, err
		}
	}
	server.newPublicAccess()
	server.newSDKState()
	if err = server.newConversationAPI(); err != nil {
		return nil, err
	}
	if err = server.newCollaborationHost(); err != nil {
		cancel()
		return nil, err
	}
	return server, nil
}

// Serve uses the already owned listener. Graceful shutdown does not complete
// until the application sessions and their tool subprocesses have closed.
func (server *Server) Serve(ctx context.Context, listener net.Listener) error {
	server.setCollaborationEndpoint(listener.Addr())
	host := &http.Server{Handler: server, ReadHeaderTimeout: 30 * time.Second}
	stopped := make(chan struct{})
	shutdownResult := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			sessionError := server.Close(shutdown)
			hostError := host.Shutdown(shutdown)
			shutdownResult <- errors.Join(sessionError, hostError)
		case <-stopped:
			shutdownResult <- nil
		}
	}()
	serveError := host.Serve(listener)
	close(stopped)
	shutdownError := <-shutdownResult
	cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cleanupError := server.Close(cleanup)
	if errors.Is(serveError, http.ErrServerClosed) {
		serveError = nil
	}
	return errors.Join(serveError, shutdownError, cleanupError)
}

func (server *Server) Close(ctx context.Context) error {
	server.mu.Lock()
	if !server.closed {
		server.closed = true
		server.cancel()
		go server.finalizeClose()
	}
	done := server.closeDone
	server.mu.Unlock()
	select {
	case <-done:
		server.mu.Lock()
		err := server.closeError
		server.mu.Unlock()
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// A caller deadline only bounds its wait. One server-owned finalizer drains
// accepted work before releasing shared/native resources; closeDone denotes
// actual completion, including after the initiating caller has timed out.
func (server *Server) finalizeClose() {
	ctx := context.Background()
	server.mu.Lock()
	var sockets []*connection
	var runtimes []*runtimeSession
	var watches []chan struct{}
	for _, group := range server.connections {
		for socket := range group {
			sockets = append(sockets, socket)
		}
	}
	for _, runtime := range server.runtimes {
		runtimes = append(runtimes, runtime)
	}
	for done := range server.workspaceWatches {
		watches = append(watches, done)
	}
	server.mu.Unlock()
	server.cancel()
	for _, socket := range sockets {
		_ = socket.ws.Close()
	}
	var failures []error
	server.waitAPIOperations()
	if server.sideQuestions != nil {
		if e := server.sideQuestions.Close(ctx); e != nil {
			failures = append(failures, e)
		}
	}
	if server.options.WhatsAppRegistration != nil {
		if err := server.options.WhatsAppRegistration.Close(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	if server.options.WechatRegistration != nil {
		if err := server.options.WechatRegistration.Close(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	if server.options.QQRegistration != nil {
		if err := server.options.QQRegistration.Close(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	if server.publicAccess != nil {
		server.publicAccess.Disable()
	}
	if err := server.closeTeamRuntime(ctx); err != nil {
		failures = append(failures, err)
	}
	if err := server.closeSDK(ctx); err != nil {
		failures = append(failures, err)
	}
	for _, runtime := range runtimes {
		if err := closeRuntime(ctx, runtime); err != nil {
			failures = append(failures, err)
		}
	}
	for _, done := range watches {
		select {
		case <-done:
		case <-ctx.Done():
			failures = append(failures, ctx.Err())
		}
	}
	if server.options.OnShutdown != nil {
		if err := server.options.OnShutdown(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	if server.collaboration != nil {
		if err := server.collaboration.service.Close(ctx); err != nil {
			failures = append(failures, err)
		}
		select {
		case <-server.collaboration.done:
		case <-ctx.Done():
			failures = append(failures, context.Cause(ctx))
		}
		server.collaboration.mu.Lock()
		clear(server.collaboration.tokens)
		server.collaboration.events = nil
		server.collaboration.mu.Unlock()
	}
	server.mu.Lock()
	clear(server.memoryLaunch)
	clear(server.knownSessions)
	clear(server.privateTitles)
	server.closeError = errors.Join(failures...)
	close(server.closeDone)
	server.mu.Unlock()
}

// Retiring a runtime first cancels its server-owned factory context. Waiting
// through opening and eviction prevents a deleted/shutdown session from
// retaining tools or being published again after Close returns successfully.
func closeRuntime(ctx context.Context, runtime *runtimeSession) error {
	runtime.mu.Lock()
	runtime.retired = true
	runtime.slashCommands = nil
	runtime.admission = nil
	runtime.cancel()
	runtime.epoch++
	if runtime.timer != nil {
		runtime.timer.Stop()
		runtime.timer = nil
	}
	runtime.mu.Unlock()
	for {
		runtime.mu.Lock()
		pending := runtime.opening
		if pending == nil {
			pending = runtime.closing
		}
		if pending != nil {
			runtime.mu.Unlock()
			select {
			case <-pending:
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		live := runtime.live
		runtime.live = nil
		if runtime.revokeCollaboration != nil {
			runtime.revokeCollaboration()
			runtime.revokeCollaboration = nil
		}
		if live == nil {
			err := runtime.closeError
			runtime.mu.Unlock()
			return errors.Join(err, awaitAdmission(ctx, runtime))
		}
		closing := make(chan struct{})
		runtime.closing = closing
		runtime.mu.Unlock()
		err := live.Close(ctx)
		runtime.mu.Lock()
		runtime.closeError = err
		runtime.closing = nil
		close(closing)
		runtime.mu.Unlock()
		return errors.Join(err, awaitAdmission(ctx, runtime))
	}
}

func awaitAdmission(ctx context.Context, runtime *runtimeSession) error {
	if runtime.admitted == nil {
		return nil
	}
	select {
	case <-runtime.admitted:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Response.json has a complete body. Explicit length allows a peer to
	// finish consuming an early response while owned ingress still receives
	// the request; do not close its keep-alive connection to terminate reads.
	writer.Header().Set("Content-Length", strconv.Itoa(body.Len()))
	writer.WriteHeader(status)
	_, _ = writer.Write(body.Bytes())
}

type apiError struct {
	status        int
	code, message string
}

func (err *apiError) Error() string   { return err.message }
func badRequest(message string) error { return &apiError{400, "BAD_REQUEST", message} }
func notFound(message string) error   { return &apiError{404, "NOT_FOUND", message} }
func methodNotAllowed(method string) error {
	return &apiError{405, "METHOD_NOT_ALLOWED", "Method " + method + " not allowed"}
}
func unsupported(resource string) error {
	return &apiError{501, "GO_MIGRATION_NOT_READY", "Go server capability is not implemented: " + resource}
}
func writeError(writer http.ResponseWriter, err error) {
	status, code, message := 500, "INTERNAL_ERROR", "Internal server error"
	var own *apiError
	var configured *settings.APIError
	var history *sessionstore.APIError
	var rewind *sessionrewind.BadRequest
	var collaboration *sessioncollaboration.APIError
	switch {
	case errors.As(err, &own):
		status, code, message = own.status, own.code, own.message
	case errors.As(err, &configured):
		status, code, message = configured.StatusCode, configured.Code, configured.Message
	case errors.As(err, &history):
		status, code, message = history.StatusCode, history.Code, history.Message
	case errors.As(err, &rewind):
		status, code, message = 400, "BAD_REQUEST", rewind.Message
	case errors.As(err, &collaboration):
		status, code, message = collaboration.Status, collaboration.Code, collaboration.Message
	}
	if message == "" {
		writeJSON(writer, status, map[string]string{"error": code})
		return
	}
	writeJSON(writer, status, map[string]string{"error": code, "message": message})
}

func segments(path string) []string {
	var result []string
	for _, value := range strings.Split(path, "/") {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.EscapedPath() == desktopoauth.OpenAICodexRedirectPath || request.URL.EscapedPath() == "/callback/openai" {
		if err := server.hahaOpenAIOAuthCallback(writer, request); err != nil {
			writeError(writer, err)
		}
		return
	}
	if request.URL.EscapedPath() == "/callback" {
		if err := server.hahaOAuthCallback(writer, request, server.options.HahaOAuth); err != nil {
			writeError(writer, err)
		}
		return
	}
	if request.URL.Path == "/health" {
		writer.Header().Set("Access-Control-Allow-Origin", "*")
		writer.Header().Set("Cache-Control", "no-store")
		writeJSON(writer, 200, map[string]string{"status": "ok", "timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z")})
		return
	}
	parts := segments(request.URL.EscapedPath())
	if publicaccess.IsControlPath(request.URL.EscapedPath()) {
		server.publicAccess.Control(writer, request)
		return
	}
	if action := collaborationAction(request.URL.EscapedPath()); action != "" {
		server.serveAPILifecycle(writer, request, false, func(writer http.ResponseWriter, request *http.Request) {
			if err := server.collaborationHTTP(writer, request, action, nil); err != nil {
				writeError(writer, err)
			}
		})
		return
	}
	if (len(parts) < 2 || parts[0] != "api") && !strings.HasPrefix(request.URL.Path, "/sdk/") && !strings.HasPrefix(request.URL.Path, "/ws/") && !strings.HasPrefix(request.URL.Path, "/proxy/") && !strings.HasPrefix(request.URL.EscapedPath(), "/preview-fs/") && !strings.HasPrefix(request.URL.EscapedPath(), "/local-file/") {
		// Source serves the non-secret H5 bootstrap before token authorization.
		server.static(writer, request)
		return
	}
	if !server.authorize(writer, request) {
		return
	}
	if strings.HasPrefix(request.URL.Path, "/ws/") {
		server.websocket(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/sdk/") {
		server.sdkWebsocket(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.EscapedPath(), "/preview-fs/") {
		server.previewFilesystem(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.EscapedPath(), "/local-file/") {
		server.localFile(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/proxy/") {
		server.proxyHTTP(writer, request)
		return
	}
	if len(parts) < 2 || parts[0] != "api" {
		writeError(writer, unsupported("provider proxy/filesystem preview transport"))
		return
	}
	server.serveAPILifecycle(writer, request, true, func(writer http.ResponseWriter, request *http.Request) {
		if remote, _ := request.Context().Value(remoteBrowserKey{}).(bool); remote && (parts[1] == "providers" || parts[1] == "settings") {
			server.remoteSettingsHTTP(writer, request, parts)
			return
		}
		server.serveAPI(writer, request, parts)
	})
}

func (server *Server) serveAPI(writer http.ResponseWriter, request *http.Request, parts []string) {
	var err error
	switch parts[1] {
	case "diagnostics":
		err = server.diagnosticsHTTP(writer, request, parts, server.options.Diagnostics)
	case "traces":
		err = server.tracesHTTP(writer, request, parts, server.options.Trace)
	case "activity-stats":
		err = server.activityStatsHTTP(writer, request, parts, server.options.ActivityStats)
	case "search":
		err = server.searchHTTP(writer, request, parts, server.options.Search)
	case "agents":
		err = server.agentsHTTP(writer, request, parts, server.options.Agents)
	case "plugins":
		if server.options.Plugins == nil {
			err = unsupported("plugins")
		} else {
			var service *pluginsapi.Service
			service, err = server.options.Plugins(request.Context(), request.URL.Query().Get("cwd"))
			if err == nil {
				err = server.pluginsHTTP(writer, request, parts, service)
			}
		}
	case "doctor":
		err = server.doctorHTTP(writer, request, parts, server.options.Doctor)
	case "computer-use":
		err = server.computerUseHTTP(writer, request, parts)
	case "haha-oauth":
		err = server.hahaOAuthHTTP(writer, request, parts, server.options.HahaOAuth)
	case "haha-openai-oauth":
		err = server.hahaOpenAIOAuthHTTP(writer, request, parts)
	case "sessions":
		err = server.sessionsHTTP(writer, request, parts)
	case "settings", "permissions":
		err = server.settingsHTTP(writer, request, parts)
	case "desktop-ui":
		if server.desktopUI == nil {
			err = unsupported("desktop-ui")
		} else {
			server.desktopUI.Handle(writer, request, parts)
		}
	case "h5-access":
		if server.h5 == nil {
			err = unsupported("h5-access")
		} else {
			server.h5.Handle(writer, request, parts)
		}
	case "models", "effort":
		err = server.modelsHTTP(writer, request, parts)
	case "providers":
		err = server.providersHTTP(writer, request, parts)
	case "tasks":
		err = server.tasksHTTP(writer, request, parts)
	case "skills":
		err = server.skillsHTTP(writer, request, parts)
	case "memory":
		if server.options.Memory == nil {
			err = unsupported("memory")
		} else {
			server.options.Memory.HandleHTTP(writer, request, parts)
		}
	case "open-targets":
		if server.options.OpenTargets == nil {
			err = unsupported("open-targets")
		} else {
			server.options.OpenTargets.HandleHTTP(writer, request, parts)
		}
	case "connectors":
		if server.options.Connectors == nil {
			err = unsupported("connectors")
		} else {
			connectors.HandleHTTP(writer, request, parts, server.options.Connectors)
		}
	case "filesystem":
		err = server.filesystemHTTP(writer, request, parts)
	case "mcp":
		err = server.mcpHTTP(writer, request, parts)
	case "status":
		if request.Method != http.MethodGet {
			err = methodNotAllowed(request.Method)
		} else if len(parts) == 2 {
			writeJSON(writer, 200, map[string]any{"status": "ok", "version": server.options.Version, "uptime": time.Since(server.startedAt).Milliseconds()})
		} else {
			err = unsupported(strings.Join(parts[1:], "/"))
		}
	case "scheduled-tasks":
		err = server.scheduledTasksHTTP(writer, request, parts)
	case "session-collaboration":
		err = server.collaborationHTTP(writer, request, "", parts[2:])
	case "voice":
		err = server.voiceHTTP(writer, request, parts, server.options.Voice)
	case "conversations":
		err = server.conversationsHTTP(writer, request, parts)
	case "workflows":
		err = server.workflowsHTTP(writer, request, parts)
	case "teams":
		err = server.teamsHTTP(writer, request, parts)
	case "haha-grok-oauth":
		err = server.hahaGrokOAuthHTTP(writer, request, parts)
	case "adapters":
		err = server.adaptersHTTP(writer, request, parts)
	case "market":
		if server.options.Market == nil {
			err = unsupported("market")
		} else {
			server.options.Market.HandleHTTP(writer, request, parts)
		}
	default:
		err = &apiError{404, "Not Found", "Unknown API resource: " + parts[1]}
	}
	if err != nil {
		writeError(writer, err)
	}
}
