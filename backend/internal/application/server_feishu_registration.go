package application

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/imregistration"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/voice"
	"os"
)

func newDesktopFeishuRegistration(o ServerOptions) (*imregistration.FeishuService, error) {
	client := o.FeishuRegistrationHTTPClient
	if client == nil {
		tls, e := tlsconfig.NewState(os.ReadFile)
		if e != nil {
			return nil, e
		}
		client, e = voice.NewHTTPClient(o.Environment, tls)
		if e != nil {
			return nil, e
		}
	}
	return imregistration.NewFeishu(imregistration.FeishuOptions{Client: client})
}
