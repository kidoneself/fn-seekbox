package cloud

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"pansou/util/json"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

type Service struct {
	centerBase   string
	centerClient *http.Client
	centerMu     sync.Mutex
	store        *store
	providers    map[string]Provider
	ctx          context.Context
	cancel       context.CancelFunc
	wake         chan struct{}
	wg           sync.WaitGroup
	accountLocks sync.Map
	loginLocks   sync.Map
	qrMu         sync.Mutex
	qrSessions   map[string]*qrLogin
	qrCommand    func(context.Context, string) *exec.Cmd
}

func NewService(dir string, providers []Provider) (*Service, error) {
	st, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{centerBase: defaultCenterURL, centerClient: centerHTTPClient(), store: st, providers: map[string]Provider{}, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1)}
	for _, p := range providers {
		s.providers[p.ID()] = p
	}
	owners, err := st.owners()
	if err == nil {
		for _, owner := range owners {
			state, readErr := st.read(owner)
			if readErr != nil {
				err = readErr
				break
			}
			recoveryNeeded := false
			for _, job := range state.Jobs {
				if job.Status == "running" {
					recoveryNeeded = true
				}
			}
			for _, check := range state.Checks {
				if check.Status == "running" || check.CleanupStatus == "running" || (check.Status == "queued" && check.Trigger != "manual" && check.ScheduledFor < midnight(time.Now()).UnixMilli()) {
					recoveryNeeded = true
				}
			}
			if !recoveryNeeded {
				continue
			}
			err = st.update(owner, func(state *userState) error {
				recoverFunctions(state)
				for id, job := range state.Jobs {
					if job.Status != "running" {
						continue
					}
					job.Status, job.Error, job.FinishedAt = "interrupted", "服务重启中断了任务，请检查网盘中的实际结果", now()
					state.Jobs[id] = job
					if sub, ok := state.Subscriptions[job.SubscriptionID]; ok {
						sub.Status, sub.LastError = "error", job.Error
						state.Subscriptions[sub.ID] = sub
					}
				}
				return nil
			})
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		cancel()
		st.db.Close()
		return nil, err
	}
	s.wg.Add(2)
	go s.run()
	go s.centerLoop()
	return s, nil
}

func (s *Service) Close() error {
	s.qrMu.Lock()
	s.cancel()
	for _, q := range s.qrSessions {
		q.cancel()
	}
	s.qrMu.Unlock()
	s.wg.Wait()
	return s.store.db.Close()
}
func now() int64    { return time.Now().UnixMilli() }
func newID() string { return rand.Text() }
func (s *Service) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func active(status string) bool { return status == "queued" || status == "running" }

func (s *Service) Providers() []ProviderInfo {
	infos := []ProviderInfo{
		{ID: "baidu", Name: "百度网盘", Website: "https://pan.baidu.com", Help: "在电脑浏览器登录自己的百度网盘，打开开发者工具的网络面板，复制 pan.baidu.com 请求中的完整 Cookie（包括 BDUSS、STOKEN）。此处连接已有登录状态，不收集账号密码；失效后需重新连接。", CredentialFields: []CredentialField{{Key: "cookie", Label: "网页登录 Cookie", Help: "仅填写本人百度网盘的完整 Cookie"}}},
		{ID: "quark", Name: "夸克网盘", Website: "https://pan.quark.cn", Help: "在电脑浏览器登录自己的夸克网盘，打开开发者工具的网络面板，复制 drive-pc.quark.cn 请求中的完整 Cookie。失效后需重新连接。", CredentialFields: []CredentialField{{Key: "cookie", Label: "网页登录 Cookie", Help: "仅填写本人夸克网盘的完整 Cookie"}}},
		{ID: "xunlei", Name: "迅雷云盘", Website: "https://pan.xunlei.com", Help: "在电脑浏览器登录自己的迅雷云盘，在开发者工具的网络面板中找到 api-pan.xunlei.com 的请求，复制对应请求头。访问令牌和验证令牌可能过期，届时需要重新连接。", CredentialFields: []CredentialField{{Key: "access_token", Label: "访问令牌", Help: "Authorization 请求头的 Bearer 后面的值"}, {Key: "captcha_token", Label: "验证令牌", Help: "X-Captcha-Token 请求头"}, {Key: "device_id", Label: "设备标识", Help: "X-Device-Id 请求头"}, {Key: "client_id", Label: "客户端标识", Help: "X-Client-Id 请求头"}}},
	}
	result := []ProviderInfo{}
	for _, info := range infos {
		if info.ID == "xunlei" {
			info.CredentialFields = append(info.CredentialFields, CredentialField{Key: "web_session", Label: "网页登录会话 JSON", Help: "仅导入本人的会话 JSON，包含 access_token、refresh_token、expires_at 和 headers。有效 refresh_token 可续期；captcha_meta 用于更新验证令牌。缺失或过期时需要重新连接。", Optional: true})
		}
		if p, ok := s.providers[info.ID]; ok {
			info.QRLogin = s.qrAvailable(info.ID)
			info.RootID = p.RootID()
			result = append(result, info)
		}
	}
	return result
}

func (s *Service) Accounts(owner string) ([]Account, error) {
	state, err := s.store.read(owner)
	if err != nil {
		return nil, err
	}
	accounts := []Account{}
	for _, a := range state.Accounts {
		accounts = append(accounts, a.Account)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].CreatedAt < accounts[j].CreatedAt })
	return accounts, nil
}

func accountBusy(state *userState, id string) bool {
	for _, check := range state.Checks {
		if check.AccountID == id && (active(check.Status) || active(check.CleanupStatus)) {
			return true
		}
	}
	for _, job := range state.Jobs {
		if job.AccountID == id && active(job.Status) {
			return true
		}
	}
	return false
}

func (s *Service) Connect(ctx context.Context, owner, id, provider, name string, credentials Credentials) (Account, error) {
	isUpdate := id != ""
	if isUpdate {
		unlock, err := s.lockAccount(ctx, owner, id)
		if err != nil {
			return Account{}, err
		}
		defer unlock()
	}
	state, err := s.store.read(owner)
	if err != nil {
		return Account{}, err
	}
	created := now()
	if id != "" {
		a, ok := state.Accounts[id]
		if !ok {
			return Account{}, ErrNotFound
		}
		if accountBusy(&state, id) {
			return Account{}, ErrBusy
		}
		provider, created = a.Provider, a.CreatedAt
	} else {
		id = newID()
	}
	p, ok := s.providers[provider]
	if !ok {
		return Account{}, errors.New("暂不支持这个网盘")
	}
	if len(credentials) == 0 || len(credentials) > 8 {
		return Account{}, errors.New("请填写网盘登录凭据")
	}
	for key, value := range credentials {
		credentials[key] = strings.TrimSpace(value)
		if len(value) > 24000 || (key != "web_session" && strings.ContainsAny(value, "\r\n\x00")) {
			return Account{}, errors.New("登录凭据格式无效，请复制完整的单行请求头")
		}
	}
	for _, info := range s.Providers() {
		if info.ID != provider {
			continue
		}
		for _, field := range info.CredentialFields {
			if field.Optional || (provider == "xunlei" && credentials["web_session"] != "") {
				continue
			}
			if credentials[field.Key] == "" {
				return Account{}, fmt.Errorf("请填写%s", field.Label)
			}
		}
	}
	// A newly submitted login is persisted only after validation and identity
	// matching; refreshes during this check update its in-memory copy.
	ctx = withCredentialCommit(ctx, func(updated Credentials) error {
		copy := Credentials{}
		for key, value := range updated {
			copy[key] = value
		}
		for key := range credentials {
			delete(credentials, key)
		}
		for key, value := range copy {
			credentials[key] = value
		}
		return nil
	})
	identity, err := p.Validate(ctx, credentials)
	if err != nil {
		return Account{}, errors.New(safeError(err, credentials))
	}
	// A new QR session is not necessarily a new cloud account. Serialize new
	// logins by validated identity, then reuse the original record so its
	// subscriptions and transfer journal keep the same account ID.
	if !isUpdate && identity.ID != "" {
		unlock, err := lockOperation(ctx, &s.loginLocks, digest(owner, provider, identity.ID))
		if err != nil {
			return Account{}, err
		}
		defer unlock()
		current, err := s.store.read(owner)
		if err != nil {
			return Account{}, err
		}
		var matched *Account
		for _, candidate := range current.Accounts {
			if candidate.Provider != provider || candidate.ProviderUserID != identity.ID {
				continue
			}
			if matched == nil || candidate.CreatedAt < matched.CreatedAt || (candidate.CreatedAt == matched.CreatedAt && candidate.ID < matched.ID) {
				copy := candidate.Account
				matched = &copy
			}
		}
		if matched != nil {
			unlockAccount, err := s.lockAccount(ctx, owner, matched.ID)
			if err != nil {
				return Account{}, err
			}
			defer unlockAccount()
			id, created, isUpdate = matched.ID, matched.CreatedAt, true
			// Scanning from the add-account entry should not replace a remark.
			name = matched.Name
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = identity.Name
	}
	if name == "" {
		name = provider
	}
	if len(name) > 160 {
		return Account{}, errors.New("账号名称过长")
	}
	secret, err := s.store.encrypt(owner, id, credentials)
	if err != nil {
		return Account{}, err
	}
	account := Account{ID: id, Provider: provider, ProviderUserID: identity.ID, Name: name, Status: "ready", AuthCheckedAt: now(), CreatedAt: created, UpdatedAt: now()}
	err = s.store.update(owner, func(state *userState) error {
		if existing, exists := state.Accounts[id]; exists {
			if existing.ProviderUserID != "" && existing.ProviderUserID != identity.ID {
				return errors.New("这不是原来的网盘账号，请使用新增账号连接，避免混用追更进度")
			}
			if accountBusy(state, id) {
				return ErrBusy
			}
			account.CreatedAt = existing.CreatedAt
		} else {
			if isUpdate {
				return ErrNotFound
			}
			if len(state.Accounts) >= 30 {
				return errors.New("最多连接 30 个网盘账号")
			}
		}
		state.Accounts[id] = storedAccount{Account: account, Secret: secret}
		return nil
	})
	if err == nil {
		s.notify()
	}
	return account, err
}

func (s *Service) account(owner, id string) (storedAccount, Provider, Credentials, error) {
	state, err := s.store.read(owner)
	if err != nil {
		return storedAccount{}, nil, nil, err
	}
	a, ok := state.Accounts[id]
	if !ok {
		return storedAccount{}, nil, nil, ErrNotFound
	}
	p, ok := s.providers[a.Provider]
	if !ok {
		return a, nil, nil, errors.New("这个网盘暂不可用")
	}
	credentials, err := s.store.decrypt(owner, a)
	return a, p, credentials, err
}

func (s *Service) CheckAccount(ctx context.Context, owner, id string) (Account, error) {
	unlock, err := s.lockAccount(ctx, owner, id)
	if err != nil {
		return Account{}, err
	}
	defer unlock()
	a, p, credentials, err := s.account(owner, id)
	if err != nil {
		return Account{}, err
	}
	ctx = s.credentialContext(ctx, owner, &a)
	identity, checkErr := p.Validate(ctx, credentials)
	if checkErr == nil && a.ProviderUserID != "" && identity.ID != a.ProviderUserID {
		checkErr = errors.New("网盘身份与原账号不符，请重新连接原账号")
	}
	err = s.store.update(owner, func(state *userState) error {
		current, ok := state.Accounts[id]
		if !ok {
			return ErrNotFound
		}
		if current.Secret != a.Secret {
			return ErrBusy
		}
		current.Status, current.LastError, current.UpdatedAt = "ready", "", now()
		current.AuthCheckedAt = now()
		if checkErr != nil {
			current.Status, current.LastError = "error", safeError(checkErr, credentials)
			if errors.Is(checkErr, ErrAuthExpired) {
				current.Status = "expired"
			}
		}
		state.Accounts[id] = current
		a = current
		return nil
	})
	return a.Account, err
}

func (s *Service) DeleteAccount(owner, id string) error {
	return s.store.update(owner, func(state *userState) error {
		for _, r := range state.Checks {
			if r.AccountID == id && r.CleanupPending {
				return errors.New("请先清理该账号遗留的测试目录")
			}
		}
		if _, ok := state.Accounts[id]; !ok {
			return ErrNotFound
		}
		if accountBusy(state, id) {
			return ErrBusy
		}
		for _, sub := range state.Subscriptions {
			if sub.AccountID == id {
				return errors.New("请先移除该账号的追更任务")
			}
		}
		delete(state.Accounts, id)
		return nil
	})
}

func (s *Service) Folders(ctx context.Context, owner, id, parent string) ([]Folder, error) {
	unlock, err := s.lockAccount(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	a, p, credentials, err := s.account(owner, id)
	if err != nil {
		return nil, err
	}
	if parent == "" {
		parent = p.RootID()
	}
	folders, err := p.ListFolders(s.credentialContext(ctx, owner, &a), credentials, parent)
	if err != nil {
		return nil, errors.New(safeError(err, credentials))
	}
	if folders == nil {
		folders = []Folder{}
	}
	return folders, nil
}

func canonicalShare(provider, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || len(raw) > 2048 {
		return "", errors.New("请使用有效的 HTTPS 网盘分享链接")
	}
	hosts := map[string]map[string]bool{"baidu": {"pan.baidu.com": true, "yun.baidu.com": true}, "quark": {"pan.quark.cn": true}, "xunlei": {"pan.xunlei.com": true}}
	if !hosts[provider][strings.ToLower(u.Hostname())] {
		return "", errors.New("分享链接与所选网盘账号不匹配")
	}
	if !strings.HasPrefix(u.Path, "/s/") || len(strings.TrimPrefix(u.Path, "/s/")) == 0 {
		return "", errors.New("请使用 /s/ 开头的网盘分享链接")
	}
	u.Host = strings.ToLower(u.Host)
	if provider == "baidu" && u.Host == "yun.baidu.com" {
		u.Host = "pan.baidu.com"
	}
	u.Fragment = ""
	return u.String(), nil
}

func validateRequest(p Provider, req *TransferRequest) error {
	canonical, err := canonicalShare(p.ID(), req.ShareURL)
	if err != nil {
		return err
	}
	req.ShareURL = canonical
	u, _ := url.Parse(canonical)
	if req.Password == "" {
		req.Password = u.Query().Get("pwd")
	}
	if len(req.Password) > 64 || len(req.Title) > 500 || len(req.DestinationID) > 2048 || len(req.DestinationName) > 500 || len(req.RequestID) > 128 {
		return errors.New("转存参数过长")
	}
	if strings.ContainsAny(req.DestinationID, "\r\n\x00") {
		return errors.New("保存目录无效")
	}
	if req.DestinationID == "" {
		req.DestinationID = p.RootID()
	}
	if req.DestinationName == "" && req.DestinationID == p.RootID() {
		req.DestinationName = "/"
	}
	if req.DestinationName != "" && (!strings.HasPrefix(req.DestinationName, "/") || path.Clean(req.DestinationName) != req.DestinationName || strings.ContainsAny(req.DestinationName, "\\\x00\r\n")) {
		return errors.New("保存目录路径无效，请重新选择目录")
	}
	if req.Title == "" {
		req.Title = "网盘分享"
	}
	return nil
}

func pruneJobs(state *userState) {
	if len(state.Jobs) < 200 {
		return
	}
	finished := []jobRecord{}
	for _, j := range state.Jobs {
		if !active(j.Status) {
			finished = append(finished, j)
		}
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].CreatedAt < finished[j].CreatedAt })
	for _, job := range finished {
		if len(state.Jobs) < 200 {
			break
		}
		delete(state.Jobs, job.ID)
	}
}

func addJob(state *userState, req TransferRequest, provider, subscriptionID string) (Job, error) {
	pending := 0
	for _, existing := range state.Jobs {
		if subscriptionID == "" && req.RequestID != "" && existing.Request.RequestID == req.RequestID {
			if existing.AccountID != req.AccountID || existing.Request.ShareURL != req.ShareURL || existing.Request.DestinationID != req.DestinationID || existing.Request.FilterAds != req.FilterAds {
				return Job{}, errors.New("请求标识已用于其他转存")
			}
			return existing.Job, nil
		}
		if active(existing.Status) {
			pending++
			if existing.AccountID == req.AccountID && existing.Request.ShareURL == req.ShareURL && existing.Request.DestinationID == req.DestinationID {
				return Job{}, ErrBusy
			}
		}
	}
	if pending >= 50 {
		return Job{}, errors.New("等待中的任务过多，请稍后再试")
	}
	pruneJobs(state)
	job := Job{ID: newID(), AccountID: req.AccountID, SubscriptionID: subscriptionID, Provider: provider, Title: req.Title, Status: "queued", CreatedAt: now()}
	state.Jobs[job.ID] = jobRecord{Job: job, Request: req}
	return job, nil
}

func (s *Service) Transfer(owner string, req TransferRequest) (Job, error) {
	a, p, _, err := s.account(owner, req.AccountID)
	if err != nil {
		return Job{}, err
	}
	if a.Status != "ready" {
		return Job{}, errors.New("请先重新连接或检查网盘账号")
	}
	if err = validateRequest(p, &req); err != nil {
		return Job{}, err
	}
	if req.RequestID == "" {
		return Job{}, errors.New("缺少请求标识，请重新打开转存窗口")
	}
	var job Job
	err = s.store.update(owner, func(state *userState) error {
		if _, ok := state.Accounts[req.AccountID]; !ok {
			return ErrNotFound
		}
		var err error
		job, err = addJob(state, req, a.Provider, "")
		return err
	})
	if err == nil {
		s.notify()
	}
	return job, err
}

func (s *Service) Subscribe(owner string, req TransferRequest, interval int) (Subscription, Job, error) {
	a, p, _, err := s.account(owner, req.AccountID)
	if err != nil {
		return Subscription{}, Job{}, err
	}
	if a.Status != "ready" {
		return Subscription{}, Job{}, errors.New("请先重新连接或检查网盘账号")
	}
	if err = validateRequest(p, &req); err != nil {
		return Subscription{}, Job{}, err
	}
	if interval == 0 {
		interval = 60
	}
	if interval < 15 || interval > 1440 {
		return Subscription{}, Job{}, errors.New("检查间隔应为 15 至 1440 分钟")
	}
	sub := Subscription{ID: newID(), TransferRequest: req, Provider: a.Provider, IntervalMinutes: interval, Enabled: true, Status: "running", CreatedAt: now()}
	var job Job
	err = s.store.update(owner, func(state *userState) error {
		if _, ok := state.Accounts[req.AccountID]; !ok {
			return ErrNotFound
		}
		for _, existing := range state.Subscriptions {
			if req.RequestID != "" && existing.RequestID == req.RequestID {
				if existing.AccountID != req.AccountID || existing.ShareURL != req.ShareURL || existing.DestinationID != req.DestinationID || existing.FilterAds != req.FilterAds {
					return errors.New("请求标识已用于其他追更")
				}
				sub = existing
				for _, previous := range state.Jobs {
					if previous.SubscriptionID == sub.ID && (job.ID == "" || previous.CreatedAt > job.CreatedAt) {
						job = previous.Job
					}
				}
				if job.ID == "" {
					return errors.New("该追更已经创建，请在我的追更中查看")
				}
				return nil
			}
			if existing.AccountID == req.AccountID && existing.ShareURL == req.ShareURL && existing.DestinationID == req.DestinationID {
				return errors.New("该分享已在这个目录中追更")
			}
		}
		if len(state.Subscriptions) >= 100 {
			return errors.New("最多添加 100 个追更任务")
		}
		var err error
		job, err = addJob(state, req, a.Provider, sub.ID)
		if err != nil {
			return err
		}
		state.Subscriptions[sub.ID] = sub
		return nil
	})
	if err == nil {
		s.notify()
	}
	return sub, job, err
}

func (s *Service) Subscriptions(owner string) ([]Subscription, error) {
	state, err := s.store.read(owner)
	if err != nil {
		return nil, err
	}
	result := []Subscription{}
	for _, sub := range state.Subscriptions {
		result = append(result, sub)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt > result[j].CreatedAt })
	return result, nil
}

func (s *Service) UpdateSubscription(owner, id string, enabled *bool, interval *int, filterAds *bool) (Subscription, error) {
	var sub Subscription
	err := s.store.update(owner, func(state *userState) error {
		var ok bool
		sub, ok = state.Subscriptions[id]
		if !ok {
			return ErrNotFound
		}
		if filterAds != nil && *filterAds != sub.FilterAds {
			for _, job := range state.Jobs {
				if job.SubscriptionID == id && active(job.Status) {
					return errors.New("请等待当前追更检查结束，再修改广告过滤设置")
				}
			}
			sub.FilterAds = *filterAds
		}
		if interval != nil {
			if *interval < 15 || *interval > 1440 {
				return errors.New("检查间隔应为 15 至 1440 分钟")
			}
			sub.IntervalMinutes = *interval
			sub.NextCheckAt = now() + int64(*interval)*60000
		}
		if enabled != nil {
			sub.Enabled = *enabled
			if *enabled {
				sub.Status = "idle"
				sub.NextCheckAt = now()
			} else {
				sub.Status = "paused"
			}
		}
		state.Subscriptions[id] = sub
		return nil
	})
	if err == nil {
		s.notify()
	}
	return sub, err
}

func (s *Service) DeleteSubscription(owner, id string) error {
	return s.store.update(owner, func(state *userState) error {
		if _, ok := state.Subscriptions[id]; !ok {
			return ErrNotFound
		}
		for _, job := range state.Jobs {
			if job.SubscriptionID == id && active(job.Status) {
				return ErrBusy
			}
		}
		delete(state.Subscriptions, id)
		return nil
	})
}

func (s *Service) CheckSubscription(owner, id string) (Job, error) {
	var job Job
	err := s.store.update(owner, func(state *userState) error {
		sub, ok := state.Subscriptions[id]
		if !ok {
			return ErrNotFound
		}
		if !sub.Enabled {
			return errors.New("请先恢复追更，再立即检查")
		}
		a, ok := state.Accounts[sub.AccountID]
		if !ok {
			return ErrNotFound
		}
		if a.Status != "ready" {
			return errors.New("请先重新连接或检查网盘账号")
		}
		var err error
		job, err = addJob(state, sub.TransferRequest, sub.Provider, id)
		if err != nil {
			return err
		}
		sub.Status = "running"
		state.Subscriptions[id] = sub
		return nil
	})
	if err == nil {
		s.notify()
	}
	return job, err
}

func (s *Service) Jobs(owner string) ([]Job, error) {
	state, err := s.store.read(owner)
	if err != nil {
		return nil, err
	}
	jobs := []Job{}
	for _, job := range state.Jobs {
		jobs = append(jobs, job.Job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt > jobs[j].CreatedAt })
	return jobs, nil
}
func (s *Service) Job(owner, id string) (Job, error) {
	state, err := s.store.read(owner)
	if err != nil {
		return Job{}, err
	}
	job, ok := state.Jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	return job.Job, nil
}

func safeError(err error, credentials Credentials) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "任务已中断，请检查网盘中的实际结果"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "网盘操作超时，请检查网盘中的实际结果"
	}
	message := err.Error()
	secrets := []string{}
	var collectJSON func(any)
	collectJSON = func(v any) {
		switch value := v.(type) {
		case string:
			if value != "" {
				secrets = append(secrets, value)
			}
		case map[string]any:
			for _, item := range value {
				collectJSON(item)
			}
		case []any:
			for _, item := range value {
				collectJSON(item)
			}
		}
	}
	for key, secret := range credentials {
		if secret == "" {
			continue
		}
		secrets = append(secrets, secret)
		if strings.Contains(strings.ToLower(key), "cookie") {
			for _, part := range strings.Split(secret, ";") {
				if _, value, ok := strings.Cut(strings.TrimSpace(part), "="); ok && value != "" {
					secrets = append(secrets, value)
				}
			}
		}
		if strings.HasPrefix(strings.ToLower(secret), "bearer ") {
			secrets = append(secrets, strings.TrimSpace(secret[7:]))
		}
		if key == "web_session" {
			var value any
			if json.Unmarshal([]byte(secret), &value) == nil {
				collectJSON(value)
			}
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		message = strings.ReplaceAll(message, secret, "[已隐藏]")
	}
	if len(message) > 600 {
		message = "网盘操作失败，请检查账号状态和分享链接"
	}
	return message
}

func digest(parts ...string) string {
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validEntry(entry Entry) bool {
	if entry.ID == "" || entry.Path == "" || entry.Name == "" || len(entry.Path) > 4096 {
		return false
	}
	if strings.ContainsAny(entry.Path, "\\\x00") || strings.HasPrefix(entry.Path, "/") {
		return false
	}
	if path.Clean(entry.Path) != entry.Path || path.Base(entry.Path) != entry.Name {
		return false
	}
	for _, part := range strings.Split(entry.Path, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	return true
}
