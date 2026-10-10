package connectors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	weburl "github.com/nlnwa/whatwg-url/url"
)

type nativeAdapter struct {
	def     protocol.JSONObject
	root    string
	runtime Runtime
	uuid    func() string
}

func NativeAdapter(def protocol.JSONObject, root string, r Runtime, uuid func() string) (Adapter, error) {
	if r.Download == nil || r.Extract == nil || r.Run == nil || r.ReadBinary == nil || r.Pins == nil || uuid == nil {
		return nil, errors.New("managed connector requires actual runtime primitives")
	}
	if _, err := managedInstallation(def, root, r); err != nil {
		return nil, err
	}
	return &nativeAdapter{def, root, r, uuid}, nil
}

func (a *nativeAdapter) Prepare(ctx context.Context, p Progress) (Installation, error) {
	return prepareManaged(ctx, a.def, a.root, p, a.runtime, a.uuid)
}
func (a *nativeAdapter) owned(i Installation) (Installation, error) {
	expected, err := managedInstallation(a.def, a.root, a.runtime)
	if err != nil {
		return expected, err
	}
	if filepath.Clean(i.Directory) != expected.Directory || filepath.Clean(i.Command) != expected.Command {
		return expected, errors.New("Invalid managed connector installation")
	}
	return expected, nil
}

const urlTerminator = `\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}"<>\\`

var authURL = regexp.MustCompile(`https://[^` + urlTerminator + `]+[` + urlTerminator + `]`)

func TrustedAuthorizationURL(id, candidate string) string {
	u, err := weburl.Parse(candidate)
	if err != nil || u.Scheme() != "https" || u.Username() != "" || u.Password() != "" || u.Port() != "" && u.Port() != "443" {
		return ""
	}
	hosts := map[string][]string{"feishu": {"accounts.feishu.cn", "open.feishu.cn", "accounts.larksuite.com", "open.larksuite.com", "passport.feishu.cn", "accounts.larkoffice.com"}, "dingtalk": {"login.dingtalk.com", "mcp.dingtalk.com"}, "wecom": {"work.weixin.qq.com"}}
	for _, host := range hosts[id] {
		if u.Hostname() == host {
			return u.Href(false)
		}
	}
	return ""
}

func (a *nativeAdapter) invoke(ctx context.Context, i Installation, args []string, p Progress) (ProcessResult, error) {
	owned, err := a.owned(i)
	if err != nil {
		return ProcessResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return ProcessResult{}, err
	}
	pins, err := a.runtime.Pins(a.def, a.runtime.Platform+"-"+a.runtime.Arch)
	if err != nil {
		return ProcessResult{}, err
	}
	bytes, err := a.runtime.ReadBinary(owned.Command)
	if err != nil {
		return ProcessResult{}, err
	}
	if err = VerifyIntegrity(bytes, pins.Binary); err != nil {
		return ProcessResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return ProcessResult{}, err
	}
	options := RunOptions{Environment: owned.Environment, Timeout: 30 * time.Second}
	if p != nil {
		options.Timeout = 300 * time.Second
		buffered, previous := "", ""
		options.OnOutput = func(text string) {
			buffered += text
			units := utf16.Encode([]rune(buffered))
			if len(units) > 8192 {
				buffered = string(utf16.Decode(units[len(units)-8192:]))
			}
			for _, match := range authURL.FindAllString(buffered, -1) {
				candidate := strings.TrimRightFunc(match, func(c rune) bool { return jsSpace(c) || strings.ContainsRune(`"<>\`, c) })
				v := TrustedAuthorizationURL(a.def.String("id"), candidate)
				if v != "" && v != previous {
					previous = v
					p("awaiting-authorization", &v)
				}
			}
		}
	}
	return a.runtime.Run(ctx, owned.Command, append(append([]string{}, owned.Args...), args...), options)
}

func jsonOutput(result ProcessResult) (protocol.JSONObject, error) {
	streams := []string{result.Stdout}
	if result.Code != 0 {
		streams = append(streams, result.Stderr)
	}
	for _, text := range streams {
		var o protocol.JSONObject
		if o.UnmarshalJSON([]byte(text)) == nil && o.IsObject() {
			return o, nil
		}
	}
	return protocol.JSONObject{}, errors.New("Unexpected connector status response")
}

func ParseConnectorCheck(id string, result ProcessResult) (Check, error) {
	if id == "wecom" {
		if result.Code != 0 {
			return Check{}, errors.New("Unable to check connector authorization")
		}
		status := trimJS(result.Stdout)
		if status != "authorized" && status != "unauthorized" {
			return Check{}, errors.New("Unexpected connector status response")
		}
		return Check{Authenticated: status == "authorized", Verification: "local"}, nil
	}
	data, err := jsonOutput(result)
	if err != nil {
		return Check{}, err
	}
	if id == "feishu" {
		failure := nested(data.Get("error"))
		if failure.String("type") == "config" && failure.String("subtype") == "not_configured" {
			return Check{Verification: "local"}, nil
		}
		if result.Code != 0 || string(data.Get("ok")) == "false" {
			return Check{}, errors.New("Unable to check connector authorization")
		}
		result := Check{Authenticated: data.String("identity") == "user" && string(data.Get("verified")) == "true", Verification: "remote"}
		label := nested(nested(data.Get("identities")).Get("user")).Get("userName")
		if units, err := protocol.DecodeJavaScriptJSONString(label); err == nil {
			clean := make([]uint16, 0, len(units))
			for _, unit := range units {
				if unit >= 32 && unit != 127 {
					clean = append(clean, unit)
				}
			}
			units = clean
			for len(units) > 0 && jsSpace(rune(units[0])) {
				units = units[1:]
			}
			for len(units) > 0 && jsSpace(rune(units[len(units)-1])) {
				units = units[:len(units)-1]
			}
			if len(units) > 160 {
				units = units[:160]
			}
			if len(units) > 0 {
				name := string(utf16.Decode(units))
				result.AccountLabel = &name
				result.AccountLabelJSON = protocol.QuoteJavaScriptUTF16(units)
			}
		}
		return result, nil
	}
	if result.Code != 0 || string(data.Get("success")) != "true" || string(data.Get("authenticated")) != "true" && string(data.Get("authenticated")) != "false" {
		return Check{}, errors.New("Unable to check connector authorization")
	}
	return Check{Authenticated: string(data.Get("authenticated")) == "true", Verification: "local"}, nil
}

func (a *nativeAdapter) Check(ctx context.Context, i Installation) (Check, error) {
	args := map[string][]string{"feishu": {"auth", "status", "--json", "--verify"}, "dingtalk": {"auth", "status", "--format", "json"}, "wecom": {"auth", "show", "--status"}}[a.def.String("id")]
	result, err := a.invoke(ctx, i, args, nil)
	if err != nil {
		return Check{}, err
	}
	return ParseConnectorCheck(a.def.String("id"), result)
}
func (a *nativeAdapter) Authenticate(ctx context.Context, i Installation, p Progress) error {
	if a.def.String("id") == "feishu" {
		status, err := a.invoke(ctx, i, []string{"auth", "status", "--json"}, nil)
		if err != nil {
			return err
		}
		data, err := jsonOutput(status)
		if err != nil {
			return err
		}
		failure := nested(data.Get("error"))
		if failure.String("type") == "config" && failure.String("subtype") == "not_configured" {
			p("configuring-account", nil)
			setup, err := a.invoke(ctx, i, []string{"config", "init", "--new", "--brand", "feishu"}, p)
			if err != nil {
				return err
			}
			if setup.Code != 0 {
				return errors.New("Connector application setup did not complete")
			}
		} else if status.Code != 0 || string(data.Get("ok")) == "false" {
			return errors.New("Unable to read existing connector configuration")
		}
	}
	p("authorizing", nil)
	args := map[string][]string{"feishu": {"auth", "login", "--recommend", "--json"}, "dingtalk": {"auth", "login", "--no-browser"}, "wecom": {"auth", "init", "--no-browser", "--noninteractive"}}[a.def.String("id")]
	result, err := a.invoke(ctx, i, args, p)
	if err != nil {
		return err
	}
	if result.Code != 0 {
		return errors.New("Connector authorization did not complete")
	}
	return nil
}
func (a *nativeAdapter) Deactivate(context.Context) error { return nil }
func (a *nativeAdapter) Remove(ctx context.Context, i Installation) error {
	owned, err := a.owned(i)
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(owned.Directory))
}
