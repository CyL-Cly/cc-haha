package application

import (
	"fmt"
	"net/http"

	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/imregistration"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func newDesktopAdapters(o ServerOptions, store *storage.Store) (*adapterconfig.Service, error) {
	return adapterconfig.NewService(adapterconfig.ServiceOptions{ConfigDir: o.ConfigDir, Store: store, LogError: func(err error) { _, _ = fmt.Fprintf(o.Stderr, "[AdapterService] %v\n", err) }})
}

func newDesktopIMRegistration(o ServerOptions, adapters *adapterconfig.Service) (*imregistration.Service, error) {
	return imregistration.New(imregistration.Options{Client: &http.Client{}, Adapters: adapters, BaseURL: o.Environment["DINGTALK_REGISTRATION_BASE_URL"], Source: o.Environment["DINGTALK_REGISTRATION_SOURCE"]})
}
