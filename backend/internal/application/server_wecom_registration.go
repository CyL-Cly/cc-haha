package application

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/imregistration"
	"github.com/NanmiCoder/cc-haha/backend/internal/tlsconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/voice"
	"os"
	"runtime"
)

func newDesktopWecomRegistration(o ServerOptions) (*imregistration.WecomService, error) {
	client := o.WecomRegistrationHTTPClient
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
	return imregistration.NewWecom(imregistration.WecomOptions{Client: client, Platform: runtime.GOOS})
}
