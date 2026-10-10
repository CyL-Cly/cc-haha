package application

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/imregistration"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/voice"
	"net/http"
	"os"
)

func newDesktopWechatRegistration(client *http.Client, environment map[string]string) (*imregistration.WechatService, error) {
	if client == nil {
		tls, e := tlsconfig.NewState(os.ReadFile)
		if e != nil {
			return nil, e
		}
		client, e = voice.NewHTTPClient(environment, tls)
		if e != nil {
			return nil, e
		}
	}
	return imregistration.NewWechat(imregistration.WechatOptions{Client: client})
}
