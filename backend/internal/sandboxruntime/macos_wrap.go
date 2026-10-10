package sandboxruntime

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
)

func numberString(value float64) string {
	raw, e := protocol.JavaScriptStringJSON(json.RawMessage(strconv.FormatFloat(value, 'g', -1, 64)))
	if e != nil {
		return fmt.Sprint(value)
	}
	var text string
	json.Unmarshal(raw, &text)
	return text
}
func ProxyEnvironment(environment map[string]string, platform string, httpPort, socksPort *float64) []string {
	tmp := environment["CLAUDE_TMPDIR"]
	if tmp == "" {
		tmp = "/tmp/claude"
	}
	out := []string{"SANDBOX_RUNTIME=1", "TMPDIR=" + tmp}
	if (httpPort == nil || *httpPort == 0) && (socksPort == nil || *socksPort == 0) {
		return out
	}
	noProxy := "localhost,127.0.0.1,::1,*.local,.local,169.254.0.0/16,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
	out = append(out, "NO_PROXY="+noProxy, "no_proxy="+noProxy)
	if httpPort != nil && *httpPort != 0 {
		port := numberString(*httpPort)
		for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			out = append(out, key+"=http://localhost:"+port)
		}
	}
	if socksPort != nil && *socksPort != 0 {
		port := numberString(*socksPort)
		out = append(out, "ALL_PROXY=socks5h://localhost:"+port, "all_proxy=socks5h://localhost:"+port)
		if platform == "darwin" || platform == "macos" {
			out = append(out, "GIT_SSH_COMMAND=ssh -o ProxyCommand='nc -X 5 -x localhost:"+port+" %h %p'")
		} else if platform == "linux" && httpPort != nil && *httpPort != 0 {
			out = append(out, "GIT_SSH_COMMAND=ssh -o ProxyCommand='socat - PROXY:localhost:%h:%p,proxyport="+numberString(*httpPort)+"'")
		}
		out = append(out, "FTP_PROXY=socks5h://localhost:"+port, "ftp_proxy=socks5h://localhost:"+port, "RSYNC_PROXY=localhost:"+port)
		docker := port
		if httpPort != nil && *httpPort != 0 {
			docker = numberString(*httpPort)
		}
		out = append(out, "DOCKER_HTTP_PROXY=http://localhost:"+docker, "DOCKER_HTTPS_PROXY=http://localhost:"+docker)
		if httpPort != nil && *httpPort != 0 {
			out = append(out, "CLOUDSDK_PROXY_TYPE=https", "CLOUDSDK_PROXY_ADDRESS=localhost", "CLOUDSDK_PROXY_PORT="+numberString(*httpPort))
		}
		out = append(out, "GRPC_PROXY=socks5h://localhost:"+port, "grpc_proxy=socks5h://localhost:"+port)
	}
	return out
}
func CommandLogTag(command, suffix string) string {
	units := utf16.Encode([]rune(command))
	if len(units) > 100 {
		units = units[:100]
	}
	text := string(utf16.Decode(units))
	return "CMD64_" + base64.StdEncoding.EncodeToString([]byte(text)) + "_END_" + suffix
}

type MacWrapOptions struct {
	Profile              MacProfileOptions
	Environment          map[string]string
	Shell, SessionSuffix string
	FindExecutable       func(string) (string, error)
}

func PrepareMac(command string, options MacWrapOptions) ([]string, error) {
	profile := options.Profile
	if !profile.NeedsNetworkRestriction && (profile.Read == nil || len(profile.Read.DenyOnly) == 0) && profile.Write == nil {
		return nil, nil
	}
	if options.FindExecutable == nil {
		return nil, errors.New("sandbox shell wrapper requires actual PATH producer")
	}
	profile.LogTag = CommandLogTag(command, options.SessionSuffix)
	text, e := MacProfile(profile)
	if e != nil {
		return nil, e
	}
	shellName := options.Shell
	if shellName == "" {
		shellName = "bash"
	}
	shell, e := options.FindExecutable(shellName)
	if e != nil || shell == "" {
		return nil, fmt.Errorf("Shell '%s' not found in PATH", shellName)
	}
	args := []string{"env"}
	args = append(args, ProxyEnvironment(options.Environment, "darwin", profile.HTTPProxyPort, profile.SOCKSProxyPort)...)
	args = append(args, "sandbox-exec", "-p", text, shell, "-c", command)
	return args, nil
}

func WrapMac(command string, options MacWrapOptions) (string, error) {
	args, e := PrepareMac(command, options)
	if e != nil {
		return "", e
	}
	if args == nil {
		return command, nil
	}
	encoded := []string{}
	for _, arg := range args {
		encoded = append(encoded, teamspawn.ShellQuote(arg))
	}
	return strings.Join(encoded, " "), nil
}
