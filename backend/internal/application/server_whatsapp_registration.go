package application

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/imregistration"
	wa "github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat"
)

func newDesktopWhatsAppRegistration(o ServerOptions) (*imregistration.WhatsAppService, error) {
	return imregistration.NewWhatsApp(imregistration.WhatsAppOptions{ConfigDir: o.ConfigDir, HomeDir: o.HomeDir, WorkDir: o.WorkDir, Environment: o.Environment, Login: imregistration.WhatsAppLoginOptions{ClientOptions: wa.NodeWhatsAppOptions{HTTPTransport: o.WhatsAppRegistrationHTTPTransport}}})
}
