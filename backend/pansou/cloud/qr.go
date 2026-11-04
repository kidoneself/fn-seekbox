package cloud

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"pansou/util/json"
)

type QRState struct {
	ID        string   `json:"id"`
	Provider  string   `json:"provider"`
	Status    string   `json:"status"`
	Message   string   `json:"message"`
	Image     string   `json:"image,omitempty"`
	QRURL     string   `json:"qr_url,omitempty"`
	ExpiresAt int64    `json:"expires_at"`
	Account   *Account `json:"account,omitempty"`
}
type qrLogin struct {
	mu                     sync.Mutex
	owner, accountID, name string
	state                  QRState
	credentials            Credentials
	cancel                 context.CancelFunc
}

func qrHelperPath() string {
	if value := os.Getenv("SEEKBOX_QR_HELPER"); value != "" {
		return value
	}
	executable, _ := os.Executable()
	return filepath.Join(filepath.Dir(executable), "..", "auth", "login.cjs")
}
func (s *Service) qrAvailable(provider string) bool {
	if s.qrCommand != nil {
		return true
	}
	if _, err := exec.LookPath("node"); err != nil {
		return false
	}
	helper := qrHelperPath()
	for _, file := range []string{helper, filepath.Join(filepath.Dir(helper), "node_modules/playwright-core/package.json")} {
		if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	if provider == "xunlei" {
		browser := os.Getenv("SEEKBOX_CHROMIUM")
		if browser == "" {
			browser = filepath.Join(filepath.Dir(helper), "chromium/chrome-headless-shell")
		}
		if info, err := os.Stat(browser); err != nil || info.Mode()&0111 == 0 {
			return false
		}
	}
	return true
}

func (s *Service) StartQR(owner, provider, accountID, name string) (QRState, error) {
	if s.providers[provider] == nil || !s.qrAvailable(provider) {
		return QRState{}, errors.New("扫码登录组件尚未就绪，请更新应用后重试")
	}
	if len(name) > 240 {
		return QRState{}, errors.New("账号备注过长")
	}
	if accountID != "" {
		state, err := s.store.read(owner)
		if err != nil {
			return QRState{}, err
		}
		account, ok := state.Accounts[accountID]
		if !ok || account.Provider != provider {
			return QRState{}, ErrNotFound
		}
	}
	s.qrMu.Lock()
	defer s.qrMu.Unlock()
	if s.ctx.Err() != nil {
		return QRState{}, ErrBusy
	}
	if s.qrSessions == nil {
		s.qrSessions = map[string]*qrLogin{}
	}
	owned := 0
	for id, q := range s.qrSessions {
		q.mu.Lock()
		if now() >= q.state.ExpiresAt {
			q.cancel()
			q.credentials = nil
			delete(s.qrSessions, id)
		} else if q.owner == owner {
			owned++
		}
		q.mu.Unlock()
	}
	if owned >= 3 || len(s.qrSessions) >= 8 {
		return QRState{}, errors.New("扫码会话较多，请先关闭旧二维码后重试")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	q := &qrLogin{owner: owner, accountID: accountID, name: strings.TrimSpace(name), cancel: cancel, state: QRState{ID: newID(), Provider: provider, Status: "starting", Message: "正在获取官方登录二维码", ExpiresAt: now() + 5*60*1000}}
	s.qrSessions[q.state.ID] = q
	initial := q.state
	s.wg.Add(1)
	go s.runQR(ctx, q)
	return initial, nil
}

func (s *Service) ownedQR(owner, id string) (*qrLogin, error) {
	s.qrMu.Lock()
	q := s.qrSessions[id]
	s.qrMu.Unlock()
	if q == nil || q.owner != owner {
		return nil, ErrNotFound
	}
	return q, nil
}
func (s *Service) QRStatus(owner, id string) (QRState, error) {
	q, err := s.ownedQR(owner, id)
	if err != nil {
		return QRState{}, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expire()
	return q.state, nil
}
func (q *qrLogin) expire() {
	if now() >= q.state.ExpiresAt && q.state.Status != "connected" {
		q.state.Status, q.state.Message = "expired", "二维码已过期，请重新获取"
		q.state.Image, q.state.QRURL, q.credentials = "", "", nil
		q.cancel()
	}
}
func (s *Service) CancelQR(owner, id string) error {
	q, err := s.ownedQR(owner, id)
	if err != nil {
		return err
	}
	q.mu.Lock()
	q.cancel()
	q.credentials = nil
	q.state.Image, q.state.QRURL = "", ""
	if q.state.Status != "connected" {
		q.state.Status, q.state.Message = "cancelled", "已取消登录"
	}
	q.mu.Unlock()
	s.qrMu.Lock()
	if s.qrSessions[id] == q {
		delete(s.qrSessions, id)
	}
	s.qrMu.Unlock()
	return nil
}
func (s *Service) CompleteQR(ctx context.Context, owner, id string, names ...string) (QRState, error) {
	q, err := s.ownedQR(owner, id)
	if err != nil {
		return QRState{}, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expire()
	if q.state.Status == "connected" {
		return q.state, nil
	}
	if q.state.Status != "ready" || q.credentials == nil {
		return QRState{}, errors.New("扫码尚未确认或已过期，请重新扫码")
	}
	if len(names) > 0 && strings.TrimSpace(names[0]) != "" {
		if len(names[0]) > 240 {
			return QRState{}, errors.New("账号备注过长")
		}
		q.name = strings.TrimSpace(names[0])
	}
	credentials := q.credentials
	q.credentials = nil
	account, err := s.Connect(ctx, owner, q.accountID, q.state.Provider, q.name, credentials)
	for key := range credentials {
		delete(credentials, key)
	}
	q.state.Image, q.state.QRURL = "", ""
	q.cancel()
	if err != nil {
		q.state.Status, q.state.Message = "failed", "账号校验未通过，请重新扫码；重新登录时请使用原来的网盘账号"
		return q.state, nil
	}
	q.state.Status, q.state.Message, q.state.Account = "connected", "登录成功，账号已保存", &account
	return q.state, nil
}

func (s *Service) runQR(ctx context.Context, q *qrLogin) {
	defer s.wg.Done()
	var command *exec.Cmd
	if s.qrCommand != nil {
		command = s.qrCommand(ctx, q.state.Provider)
	} else {
		command = exec.CommandContext(ctx, "node", qrHelperPath(), q.state.Provider)
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process != nil {
			return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	command.WaitDelay = 2 * time.Second
	command.Stderr = io.Discard
	pipe, err := command.StdoutPipe()
	if err == nil {
		err = command.Start()
	}
	if err != nil {
		q.mu.Lock()
		q.state.Status, q.state.Message = "failed", "扫码组件启动失败，请检查应用安装是否完整"
		q.mu.Unlock()
		return
	}
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		var event struct {
			Status, Message, Image string
			QRURL                  string      `json:"qr_url"`
			Credentials            Credentials `json:"credentials"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		q.mu.Lock()
		if ctx.Err() != nil || q.state.Status == "cancelled" {
			q.mu.Unlock()
			continue
		}
		switch event.Status {
		case "starting", "waiting", "scanned", "verifying", "expired", "failed":
			q.state.Status = event.Status
			// The helper is packaged code, but never forward arbitrary diagnostics.
			if len(event.Message) <= 500 {
				q.state.Message = event.Message
			}
			if strings.HasPrefix(event.Image, "data:image/png;base64,") && len(event.Image) < 800000 {
				q.state.Image = event.Image
			}
			if u, e := url.Parse(event.QRURL); e == nil && u.Scheme == "https" && u.Host == "su.quark.cn" && u.User == nil && len(event.QRURL) < 4096 {
				q.state.QRURL = event.QRURL
			}
		case "ready":
			if len(event.Credentials) > 0 && len(scanner.Bytes()) <= 64<<10 {
				q.credentials = event.Credentials
				q.state.Status, q.state.Message = "ready", "手机已确认，正在校验并保存账号"
				q.state.Image, q.state.QRURL = "", ""
			}
		}
		q.mu.Unlock()
	}
	if scanner.Err() != nil {
		q.cancel()
	}
	_ = command.Wait()
	q.mu.Lock()
	if q.state.Status != "ready" && q.state.Status != "connected" && q.state.Status != "cancelled" && q.state.Status != "expired" && q.state.Status != "failed" {
		q.state.Status, q.state.Message = "failed", "扫码未完成，请刷新二维码重试"
	}
	q.mu.Unlock()
	// Even if the browser has finished, discard unclaimed credentials on timeout.
	select {
	case <-ctx.Done():
	}
	q.mu.Lock()
	q.credentials = nil
	q.expire()
	q.mu.Unlock()
}
