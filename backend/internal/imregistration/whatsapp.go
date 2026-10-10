package imregistration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/google/uuid"
)

type WhatsAppFiles interface {
	Lstat(string) (os.FileInfo, error)
	ReadDir(string) ([]os.DirEntry, error)
	MkdirAll(string, os.FileMode) error
	RemoveAll(string) error
	Rename(string, string) error
}
type nativeWhatsAppFiles struct{}

func (nativeWhatsAppFiles) Lstat(p string) (os.FileInfo, error)     { return os.Lstat(p) }
func (nativeWhatsAppFiles) ReadDir(p string) ([]os.DirEntry, error) { return os.ReadDir(p) }
func (nativeWhatsAppFiles) MkdirAll(p string, m os.FileMode) error  { return os.MkdirAll(p, m) }
func (nativeWhatsAppFiles) RemoveAll(p string) error                { return os.RemoveAll(p) }
func (nativeWhatsAppFiles) Rename(a, b string) error                { return os.Rename(a, b) }

type WhatsAppOptions struct {
	ConfigDir, HomeDir, WorkDir string
	Environment                 map[string]string
	Login                       WhatsAppLoginOptions
	Files                       WhatsAppFiles
}
type whatsAppStaging struct {
	directory, target string
	created           time.Time
}
type WhatsAppService struct {
	mu        sync.Mutex
	o         WhatsAppOptions
	login     *WhatsAppLoginService
	staging   map[string]whatsAppStaging
	closed    bool
	wg        sync.WaitGroup
	closeDone chan struct{}
}

func NewWhatsApp(o WhatsAppOptions) (*WhatsAppService, error) {
	if o.Files == nil {
		o.Files = nativeWhatsAppFiles{}
	}
	if !filepath.IsAbs(o.ConfigDir) {
		return nil, errors.New("WhatsApp requires explicit configuration directory")
	}
	if o.Login.Now == nil {
		o.Login.Now = time.Now
	}
	if o.Login.UUID == nil {
		o.Login.UUID = uuid.NewString
	}
	s := &WhatsAppService{o: o, login: NewWhatsAppLogin(o.Login), staging: map[string]whatsAppStaging{}, closeDone: make(chan struct{})}
	entries, _ := s.o.Files.ReadDir(s.root())
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".login-") {
			continue
		}
		name := filepath.Join(s.root(), entry.Name())
		stat, err := s.o.Files.Lstat(name)
		if err == nil && stat.Mode()&os.ModeSymlink == 0 && o.Login.Now().Sub(stat.ModTime()) > 3*time.Minute {
			_ = s.remove(name)
		}
	}
	return s, nil
}
func (s *WhatsAppService) beginOperation() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("WhatsApp registration service closed")
	}
	s.wg.Add(1)
	return nil
}
func (s *WhatsAppService) root() string           { return filepath.Join(s.o.ConfigDir, "whatsapp-auth") }
func (s *WhatsAppService) DefaultAuthDir() string { return filepath.Join(s.root(), "default") }
func (s *WhatsAppService) managed(name string) bool {
	abs, err := filepath.Abs(name)
	return err == nil && filepath.Dir(abs) == s.root() && filepath.Base(abs) != ""
}
func (s *WhatsAppService) remove(name string) error {
	if !s.managed(name) {
		return nil
	}
	stat, err := s.o.Files.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if stat.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	return s.o.Files.RemoveAll(name)
}
func (s *WhatsAppService) configuredAuthDir() (string, error) {
	cfg := imruntime.LoadPlatformConfig("whatsapp", s.o.HomeDir, s.o.WorkDir, s.o.Environment, func(string) {})
	if cfg.Err != nil {
		return "", cfg.Err
	}
	return imruntime.WhatsAppAuthDirectory(cfg)
}
func (s *WhatsAppService) Begin(ctx context.Context) (protocol.JSONObject, error) {
	if e := s.beginOperation(); e != nil {
		return protocol.JSONObject{}, e
	}
	defer s.wg.Done()
	s.mu.Lock()
	var expired []string
	for key, dirs := range s.staging {
		if s.o.Login.Now().Sub(dirs.created) > 3*time.Minute {
			delete(s.staging, key)
			expired = append(expired, dirs.directory)
		}
	}
	s.mu.Unlock()
	for _, name := range expired {
		if err := s.remove(name); err != nil {
			return protocol.JSONObject{}, err
		}
	}
	target, err := s.configuredAuthDir()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if !s.managed(target) {
		target = s.DefaultAuthDir()
	}
	staging := filepath.Join(s.root(), ".login-"+s.o.Login.UUID())
	if err = s.login.Logout(staging); err != nil {
		return protocol.JSONObject{}, err
	}
	if err = s.o.Files.RemoveAll(staging); err != nil {
		return protocol.JSONObject{}, err
	}
	result, err := s.login.Start(ctx, staging)
	if err != nil {
		_ = s.remove(staging)
		return protocol.JSONObject{}, err
	}
	s.mu.Lock()
	s.staging[result.String("sessionKey")] = whatsAppStaging{staging, target, s.o.Login.Now()}
	s.mu.Unlock()
	if err = s.withQR(&result); err != nil {
		return protocol.JSONObject{}, err
	}
	return result, nil
}
func (s *WhatsAppService) withQR(result *protocol.JSONObject) error {
	if qr := result.String("qr"); qr != "" {
		image, err := QRDataURL(qr)
		if err != nil {
			// Original createQrDataUrl catches encoder failures; the login itself
			// remains valid and its response simply omits qrDataUrl.
			return nil
		}
		result.Set("qrDataUrl", encoded(image))
	}
	return nil
}
func (s *WhatsAppService) promote(staging, target string) error {
	if !s.managed(staging) || !s.managed(target) {
		return internal("WhatsApp authentication directory is invalid")
	}
	if err := s.o.Files.MkdirAll(s.root(), 0700); err != nil {
		return err
	}
	backup := filepath.Join(s.root(), ".backup-"+s.o.Login.UUID())
	stat, err := s.o.Files.Lstat(target)
	exists := err == nil && stat.Mode()&os.ModeSymlink == 0
	if exists {
		if err = s.o.Files.Rename(target, backup); err != nil {
			return err
		}
	}
	if err = s.o.Files.Rename(staging, target); err == nil {
		if err = s.o.Files.RemoveAll(backup); err == nil {
			return nil
		}
	}
	_ = s.o.Files.RemoveAll(target)
	if exists {
		_ = s.o.Files.Rename(backup, target)
	}
	return internal("Failed to activate WhatsApp authentication")
}

// A connected result carries the original producer's trusted binding fields;
// only the desktop API feeds them into its atomic adapter configuration store.
func (s *WhatsAppService) Poll(key string) (protocol.JSONObject, error) {
	if e := s.beginOperation(); e != nil {
		return protocol.JSONObject{}, e
	}
	defer s.wg.Done()
	s.mu.Lock()
	dirs, exists := s.staging[key]
	s.mu.Unlock()
	if !exists {
		return record(field("connected", false), field("status", "expired"), field("message", "WhatsApp login session expired. Generate a new QR code.")), nil
	}
	result, err := s.login.Poll(key)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if string(result.Get("connected")) == "true" {
		s.mu.Lock()
		delete(s.staging, key)
		s.mu.Unlock()
		if err = s.promote(dirs.directory, dirs.target); err != nil {
			return protocol.JSONObject{}, err
		}
		result.Set("authDir", encoded(dirs.target))
		return result, nil
	}
	if status := result.String("status"); status == "expired" || status == "error" {
		s.mu.Lock()
		delete(s.staging, key)
		s.mu.Unlock()
		if err = s.remove(dirs.directory); err != nil {
			return protocol.JSONObject{}, err
		}
	}
	if err = s.withQR(&result); err != nil {
		return protocol.JSONObject{}, err
	}
	return result, nil
}
func (s *WhatsAppService) Unbind() error {
	if e := s.beginOperation(); e != nil {
		return e
	}
	defer s.wg.Done()
	name, err := s.configuredAuthDir()
	if err != nil {
		return err
	}
	if !s.managed(name) {
		return nil
	}
	if err = s.login.Logout(name); err != nil {
		return err
	}
	return s.o.Files.RemoveAll(name)
}
func (s *WhatsAppService) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		go func() {
			_ = s.login.Close(context.Background())
			s.wg.Wait()
			close(s.closeDone)
		}()
	}
	done := s.closeDone
	s.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
