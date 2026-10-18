package mcpauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type FlowOptions struct {
	SkipBrowserOpen      bool
	OnWaitingForCallback func(func(string))
	// OnCallbackListener observes the actual listener without taking ownership.
	OnCallbackListener func(net.Listener)
}
type callbackReply struct {
	code string
	err  error
}

func (s *Service) Authenticate(ctx context.Context, name string, config protocol.JSONObject, onURL func(string) error) error {
	return s.AuthenticateWithOptions(ctx, name, config, onURL, FlowOptions{SkipBrowserOpen: true})
}
func (s *Service) AuthenticateWithOptions(ctx context.Context, name string, config protocol.JSONObject, onURL func(string) error, options FlowOptions) (flowErr error) {
	if truthy(object(config.Get("oauth")).Get("xaa")) {
		if !envTruthy(s.options.Environment["CLAUDE_CODE_ENABLE_XAA"]) {
			return fmt.Errorf("XAA is not enabled (set CLAUDE_CODE_ENABLE_XAA=1). Remove 'oauth.xaa' from server '%s' to use the standard consent flow.", name)
		}
		fields := flowFields(config)
		fields.Set("isOAuthFlow", raw(true))
		fields.Set("authMethod", quote("xaa"))
		s.event("tengu_mcp_oauth_flow_start", fields)
		return s.authenticateXAA(ctx, name, config, onURL, options)
	}
	provider, err := s.Provider(ctx, name, config)
	if err != nil {
		return err
	}
	cached, err := s.entry(ctx, provider.key)
	if err != nil {
		return err
	}
	scope := cached.String("stepUpScope")
	resourceURL := object(cached.Get("discoveryState")).String("resourceMetadataUrl")
	if resourceURL != "" {
		if _, err = parseURL(resourceURL); err != nil {
			resourceURL = ""
		}
	}
	if err = s.ClearTokens(ctx, name, config); err != nil {
		return err
	}
	id, uuidErr := s.flowUUID()
	if uuidErr != nil {
		return uuidErr
	}
	fields := flowFields(config)
	fields.Set("isOAuthFlow", raw(true))
	fields.Set("flowAttemptId", quote(id))
	s.event("tengu_mcp_oauth_flow_start", fields)
	codeObtained := false
	defer func() { s.flowEnd(config, id, codeObtained, flowErr) }()
	port, err := s.callbackPort(config)
	if err != nil {
		return err
	}
	provider.redirect = fmt.Sprintf("http://localhost:%d/callback", port)
	provider.handleRedirect = true
	provider.skipBrowser = options.SkipBrowserOpen
	provider.onURL = onURL
	if metadata, err := s.fetchAuthMetadata(ctx, name, config.String("url"), object(config.Get("oauth")).String("authServerMetadataUrl"), resourceURL); err == nil && metadata != nil {
		provider.SetMetadata(*metadata)
	}
	state, err := provider.State()
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return AuthenticationCancelledError{}
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return s.callbackListenError(port, err)
	}
	flowCtx, cancel := context.WithTimeout(ctx, FlowTimeout)
	defer cancel()
	completed := make(chan callbackReply, 1)
	var once sync.Once
	complete := func(code string, err error) { once.Do(func() { completed <- callbackReply{code, err} }) }
	var server *http.Server
	server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			<-flowCtx.Done()
			return
		}
		query := r.URL.Query()
		code := strings.Join(query["code"], ",")
		stateValues := query["state"]
		errorCode := strings.Join(query["error"], ",")
		description := strings.Join(query["error_description"], ",")
		uri := strings.Join(query["error_uri"], ",")
		w.Header().Set("Content-Type", "text/html")
		if errorCode == "" && (len(stateValues) != 1 || stateValues[0] != state) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte("<h1>Authentication Error</h1><p>Invalid state parameter. Please try again.</p><p>You can close this window.</p>"))
			complete("", errors.New("OAuth state mismatch - possible CSRF attack"))
			return
		}
		if errorCode != "" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte("<h1>Authentication Error</h1><p>" + sanitizeCallbackHTML(errorCode) + ": " + sanitizeCallbackHTML(description) + "</p><p>You can close this window.</p>"))
			message := "OAuth error: " + errorCode
			if description != "" {
				message += " - " + description
			}
			if uri != "" {
				message += " (See: " + uri + ")"
			}
			complete("", errors.New(message))
			return
		}
		if code != "" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte("<h1>Authentication Successful</h1><p>You can close this window. Return to Claude Code.</p>"))
			complete(code, nil)
			return
		}
		<-flowCtx.Done()
	})}
	defer func() { cancel(); _ = server.Close(); _ = listener.Close() }()
	if options.OnCallbackListener != nil {
		options.OnCallbackListener(listener)
	}
	if options.OnWaitingForCallback != nil {
		options.OnWaitingForCallback(func(address string) {
			u, err := parseURL(address)
			if err != nil {
				return
			}
			query := u.SearchParams()
			code := query.Get("code")
			if failure := query.Get("error"); failure != "" {
				complete("", fmt.Errorf("OAuth error: %s - %s", failure, query.Get("error_description")))
				return
			}
			if code == "" {
				return
			}
			if query.Get("state") != state {
				complete("", errors.New("OAuth state mismatch - possible CSRF attack"))
				return
			}
			complete(code, nil)
		})
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			complete("", s.callbackListenError(port, err))
		}
	}()
	go func() {
		result, err := s.auth(flowCtx, provider, authOptions{scope: scope, resourceMetadataURL: resourceURL})
		if err != nil {
			complete("", fmt.Errorf("SDK auth failed: %s", err.Error()))
			return
		}
		if result != "REDIRECT" {
			s.debug(name, "Unexpected auth result, expected REDIRECT: "+result)
		}
	}()
	var code string
	select {
	case reply := <-completed:
		_ = listener.Close()
		if reply.err != nil {
			return reply.err
		}
		code = reply.code
	case <-flowCtx.Done():
		if ctx.Err() != nil {
			return AuthenticationCancelledError{}
		}
		return errors.New("Authentication timeout")
	}
	codeObtained = true
	result, err := s.auth(ctx, provider, authOptions{authorizationCode: &code, resourceMetadataURL: resourceURL})
	if err != nil {
		var oauth *OAuthError
		if errors.As(err, &oauth) && oauth.Code == "invalid_client" && strings.Contains(oauth.Message, "Client not found") {
			_ = provider.InvalidateCredentials(ctx, "client")
		}
		return err
	}
	if result != "AUTHORIZED" {
		return errors.New("Unexpected auth result: " + result)
	}
	return nil
}

var leadingPort = regexp.MustCompile(`^[\t\n\v\f\r ]*[+]?([0-9]+)`)

func (s *Service) callbackPort(config protocol.JSONObject) (int, error) {
	if value := object(config.Get("oauth")).Get("callbackPort"); value != nil {
		var port int
		if err := jsonDecode(value, &port); err != nil {
			return 0, err
		}
		return port, nil
	}
	if match := leadingPort.FindStringSubmatch(s.options.Environment["MCP_OAUTH_CALLBACK_PORT"]); match != nil {
		if port, err := strconv.Atoi(match[1]); err == nil && port > 0 {
			return port, nil
		}
	}
	min, max := 49152, 65535
	if s.options.Platform == "windows" || s.options.Platform == "win32" {
		min, max = 39152, 49151
	}
	for attempt := 0; attempt < 100; attempt++ {
		port := min + int(s.options.RandomFloat()*float64(max-min+1))
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			_ = listener.Close()
			return port, nil
		}
	}
	listener, err := net.Listen("tcp", ":3118")
	if err == nil {
		_ = listener.Close()
		return 3118, nil
	}
	return 0, errors.New("No available ports for OAuth redirect")
}
func (s *Service) callbackListenError(port int, err error) error {
	if errors.Is(err, os.ErrExist) || strings.Contains(strings.ToLower(err.Error()), "address already in use") {
		command := fmt.Sprintf("lsof -ti:%d -sTCP:LISTEN", port)
		if s.options.Platform == "windows" || s.options.Platform == "win32" {
			command = fmt.Sprintf("netstat -ano | findstr :%d", port)
		}
		return fmt.Errorf("OAuth callback port %d is already in use — another process may be holding it. Run `%s` to find it.", port, command)
	}
	return fmt.Errorf("OAuth callback server failed: %s", err.Error())
}
func (s *Service) defaultOpenBrowser(ctx context.Context, address string) (bool, error) {
	u, err := parseURL(address)
	if err != nil || u.Scheme() != "http" && u.Scheme() != "https" {
		return false, nil
	}
	command := s.options.Environment["BROWSER"]
	args := []string{address}
	if s.options.Platform == "windows" || s.options.Platform == "win32" {
		if command == "" {
			command = "rundll32"
			args = []string{"url,OpenURL", address}
		} else {
			args = []string{"\"" + address + "\""}
		}
	} else if command == "" {
		command = "xdg-open"
		if s.options.Platform == "darwin" {
			command = "open"
		}
	}
	if !filepath.IsAbs(command) {
		found := ""
		for _, dir := range filepath.SplitList(s.options.Environment["PATH"]) {
			candidates := []string{filepath.Join(dir, command)}
			if s.options.Platform == "windows" || s.options.Platform == "win32" {
				extensions := s.options.Environment["PATHEXT"]
				if extensions == "" {
					extensions = ".COM;.EXE;.BAT;.CMD"
				}
				if filepath.Ext(command) == "" {
					for _, extension := range strings.Split(extensions, ";") {
						candidates = append(candidates, filepath.Join(dir, command+strings.ToLower(extension)), filepath.Join(dir, command+strings.ToUpper(extension)))
					}
				}
			}
			for _, candidate := range candidates {
				if info, e := os.Stat(candidate); e == nil && info.Mode().IsRegular() {
					found = candidate
					break
				}
			}
			if found != "" {
				break
			}
		}
		if found == "" {
			return false, nil
		}
		command = found
	}
	process := exec.CommandContext(ctx, command, args...)
	process.Dir = s.options.CWD
	process.Env = []string{}
	for key, value := range s.options.Environment {
		process.Env = append(process.Env, key+"="+value)
	}
	return process.Run() == nil, nil
}
func base64Text(value string) string                     { return base64.StdEncoding.EncodeToString([]byte(value)) }
func jsonDecode(value []byte, target any) error          { return json.Unmarshal(value, target) }
func callbackValues(query url.Values, key string) string { return strings.Join(query[key], ",") }
