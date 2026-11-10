package cloud

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"
)

type functionFailure struct{ code, message string }

func (e *functionFailure) Error() string { return e.message }

var shanghai = time.FixedZone("Asia/Shanghai", 8*3600)
var checkStages = []string{"fetch_source", "validate_account", "prepare_target", "transfer", "verify_transfer", "delete", "verify_cleanup"}

func midnight(t time.Time) time.Time {
	t = t.In(shanghai)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, shanghai)
}
func dailyFunctionCheckAt(t time.Time) time.Time { return midnight(t).Add(time.Second) }

func nextFunctionCheckAt(t time.Time) time.Time {
	next := dailyFunctionCheckAt(t)
	if !t.Before(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
func newFunctionCheck(account storedAccount, trigger string, slot int64) functionRecord {
	r := functionRecord{FunctionCheck: FunctionCheck{ID: newID(), AccountID: account.ID, Provider: account.Provider, Status: "queued", Trigger: trigger, ScheduledFor: slot, CreatedAt: now(), AuthStatus: "unknown"}}
	for _, stage := range checkStages {
		r.Stages = append(r.Stages, CheckStage{Stage: stage, Status: "skipped"})
	}
	return r
}
func checkBusy(st *userState, id string) bool {
	for _, r := range st.Checks {
		if r.AccountID == id && (active(r.Status) || r.CleanupPending) {
			return true
		}
	}
	return false
}
func (s *Service) QueueFunctionCheck(owner, id string) (FunctionCheck, error) {
	var r functionRecord
	err := s.store.update(owner, func(st *userState) error {
		a, ok := st.Accounts[id]
		if !ok {
			return ErrNotFound
		}
		if checkBusy(st, id) {
			return errors.New("已有检查正在执行或测试目录待清理，请先查看检查记录")
		}
		if _, ok := s.providers[a.Provider].(checkFileManager); !ok {
			return errors.New("该网盘暂不支持闭环功能检查")
		}
		r = newFunctionCheck(a, "manual", 0)
		st.Checks[r.ID] = r
		pruneChecks(st)
		return nil
	})
	if err == nil {
		s.notify()
	}
	return r.FunctionCheck, err
}
func pruneChecks(st *userState) {
	if len(st.Checks) <= 300 {
		return
	}
	records := []functionRecord{}
	for _, r := range st.Checks {
		if !active(r.Status) && !r.CleanupPending {
			records = append(records, r)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt < records[j].CreatedAt })
	for _, r := range records {
		if len(st.Checks) <= 300 {
			break
		}
		delete(st.Checks, r.ID)
	}
}
func (s *Service) FunctionChecks(owner string) ([]FunctionCheck, error) {
	st, err := s.store.read(owner)
	if err != nil {
		return nil, err
	}
	out := []FunctionCheck{}
	for _, r := range st.Checks {
		out = append(out, r.FunctionCheck)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}
func (s *Service) scheduleFunctions(at time.Time) {
	// The reporting slot stays at midnight; execution starts one second later.
	if at.Before(dailyFunctionCheckAt(at)) {
		return
	}
	owners, err := s.store.owners()
	if err != nil {
		return
	}
	slot := midnight(at).UnixMilli()
	for _, owner := range owners {
		_ = s.store.update(owner, func(st *userState) error {
			if !st.Center.Config.DailyEnabled {
				return nil
			}
			for id, a := range st.Accounts {
				if slot <= max(st.Center.Config.EnabledSince, a.CreatedAt) || slot <= st.Center.LastSlots[id] || checkBusy(st, id) {
					continue
				}
				trigger := "daily"
				if at.Sub(time.UnixMilli(slot)) > 10*time.Minute {
					trigger = "catch_up"
				}
				r := newFunctionCheck(a, trigger, slot)
				st.Checks[r.ID] = r
				st.Center.LastSlots[id] = slot
			}
			pruneChecks(st)
			return nil
		})
	}
}
func (s *Service) nextFunction() (string, functionRecord, bool) {
	owners, err := s.store.owners()
	if err != nil {
		return "", functionRecord{}, false
	}
	var chosen functionRecord
	selected := ""
	for _, owner := range owners {
		st, err := s.store.read(owner)
		if err != nil {
			continue
		}
		for _, r := range st.Checks {
			if (r.Status == "queued" || r.CleanupRequested) && (selected == "" || r.CreatedAt < chosen.CreatedAt) {
				chosen = r
				selected = owner
			}
		}
	}
	return selected, chosen, selected != ""
}
func (s *Service) saveFunction(owner string, r functionRecord) error {
	return s.store.update(owner, func(st *userState) error { st.Checks[r.ID] = r; return nil })
}
func checkEventIdentity(r functionRecord) map[string]any {
	return map[string]any{"run_id": r.ID, "account_ref": r.AccountID, "provider": r.Provider, "trigger": r.Trigger, "scheduled_for": stamp(r.ScheduledFor), "attempt": 1}
}
func finishFunction(st *userState, r functionRecord) {
	data := checkEventIdentity(r)
	data["source_id"], data["source_version"] = r.SourceID, r.SourceVersion
	data["auth_status"], data["result"] = r.AuthStatus, r.Status
	var failed, code any
	for _, stage := range r.Stages {
		if stage.Status == "failed" || stage.Status == "unknown" {
			failed = stage.Stage
			break
		}
	}
	if r.ErrorCode != "" {
		code = r.ErrorCode
	}
	data["failed_stage"], data["error_code"] = failed, code
	data["duration_ms"] = max(int64(0), r.FinishedAt-r.StartedAt)
	data["cleanup_pending"], data["deletion_mode"], data["stages"] = r.CleanupPending, checkDeletionMode(r.Provider), r.Stages
	st.Checks[r.ID] = r
	enqueueCenterEvent(st, "account_check_finished", data)
}
func checkDeletionMode(provider string) string {
	if provider == "quark" {
		return "recycle_bin"
	}
	return "unknown"
}

type checkSource struct {
	Provider  string     `json:"provider"`
	URL       string     `json:"share_url"`
	Password  string     `json:"password"`
	MaxSize   int64      `json:"max_file_size_bytes"`
	ID        string     `json:"source_id"`
	Version   string     `json:"source_version"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (s *Service) fetchCheckSource(ctx context.Context, provider string) (checkSource, error) {
	var src checkSource
	err := s.centerRequest(ctx, "GET", "/api/check-link?provider="+url.QueryEscape(provider), "", nil, &src)
	if err != nil {
		return src, err
	}
	if src.MaxSize == 0 {
		src.MaxSize = 1 << 20
	}
	u, err := url.Parse(src.URL)
	host := map[string]string{"baidu": "pan.baidu.com", "quark": "pan.quark.cn", "xunlei": "pan.xunlei.com"}[provider]
	if err != nil || u.Scheme != "https" || u.Host != host || u.User != nil || !strings.HasPrefix(u.Path, "/s/") || src.Provider != provider || src.ID == "" || src.Version == "" || len(src.ID) > 100 || len(src.Version) > 100 || src.MaxSize <= 0 || src.MaxSize > 10<<20 {
		return src, errors.New("测试源数据无效，请在管理中心检查配置")
	}
	if src.ExpiresAt != nil && !src.ExpiresAt.After(time.Now()) {
		return src, errors.New("测试分享配置已过期")
	}
	return src, nil
}

// The source is a small-file share, not a configured path. Ignore legacy
// file_path metadata and choose one eligible file without writing to the share.
func selectCheckFile(entries []Entry, maxSize int64) (Entry, error) {
	var selected Entry
	found, valid := false, false
	for _, entry := range entries {
		if entry.IsDir || !validEntry(entry) || entry.Size < 0 {
			continue
		}
		valid = true
		if entry.Size > maxSize {
			continue
		}
		if !found || entry.Size < selected.Size || (entry.Size == selected.Size && entry.Path < selected.Path) {
			selected, found = entry, true
		}
	}
	if found {
		return selected, nil
	}
	if valid {
		return Entry{}, &functionFailure{"TEST_FILE_TOO_LARGE", "分享中的文件超过测试大小限制"}
	}
	return Entry{}, &functionFailure{"TEST_FILE_NOT_FOUND", "测试分享中没有可转存的文件"}
}

func (s *Service) executeFunction(owner string, r functionRecord) {
	if r.CleanupRequested {
		s.executeCleanup(owner, r)
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	defer cancel()
	unlock, err := s.lockAccount(ctx, owner, r.AccountID)
	if err != nil {
		return
	}
	defer unlock()
	r.Status = "running"
	r.StartedAt = now()
	if err = s.store.update(owner, func(st *userState) error {
		st.Checks[r.ID] = r
		enqueueCenterEvent(st, "account_check_started", checkEventIdentity(r))
		return nil
	}); err != nil {
		return
	}
	credentials := Credentials{}
	failedStage := -1
	stage := func(i int, code string, fn func() error) bool {
		if failedStage >= 0 {
			return false
		}
		start := now()
		r.Stages[i].Status = "unknown"
		if err = s.saveFunction(owner, r); err == nil {
			err = fn()
		}
		r.Stages[i].DurationMS = now() - start
		if err != nil {
			r.Stages[i].Status = "failed"
			r.ErrorCode = code
			var failure *functionFailure
			if errors.As(err, &failure) {
				code = failure.code
				r.ErrorCode = code
			}
			var remote *centerError
			if errors.As(err, &remote) && (remote.Code == "SOURCE_DISABLED" || remote.Code == "SOURCE_NOT_CONFIGURED" || remote.Code == "SOURCE_EXPIRED") {
				code = remote.Code
				r.ErrorCode = code
			}
			r.Error = safeError(err, credentials)
			r.Stages[i].ErrorCode = code
			failedStage = i
			if errors.Is(err, ErrAuthExpired) {
				r.AuthStatus = "expired"
				r.ErrorCode = "AUTH_EXPIRED"
				r.Stages[i].ErrorCode = "AUTH_EXPIRED"
				_ = s.store.update(owner, func(st *userState) error {
					if a, ok := st.Accounts[r.AccountID]; ok {
						a.Status = "expired"
						a.LastError = r.Error
						a.AuthCheckedAt = now()
						st.Accounts[a.ID] = a
					}
					return nil
				})
			}
		} else {
			r.Stages[i].Status = "passed"
		}
		if e := s.store.update(owner, func(st *userState) error {
			st.Checks[r.ID] = r
			var errorCode any
			if r.Stages[i].ErrorCode != "" {
				errorCode = r.Stages[i].ErrorCode
			}
			enqueueCenterEvent(st, "account_check_stage_finished", map[string]any{
				"run_id": r.ID, "stage": r.Stages[i].Stage, "status": r.Stages[i].Status,
				"duration_ms": r.Stages[i].DurationMS, "error_code": errorCode,
			})
			return nil
		}); e != nil {
			failedStage = i
			r.ErrorCode = "LOCAL_STORAGE_FAILED"
			r.Error = "检查记录保存失败，已停止后续写入"
			r.Stages[i].Status = "unknown"
			return false
		}
		return err == nil
	}
	var source checkSource
	var account storedAccount
	var p Provider
	var manager checkFileManager
	var share Share
	var entry Entry
	stage(0, "SOURCE_UNAVAILABLE", func() error {
		var e error
		source, e = s.fetchCheckSource(ctx, r.Provider)
		if e == nil {
			r.SourceID, r.SourceVersion = source.ID, source.Version
		}
		return e
	})
	stage(1, "PROVIDER_UNAVAILABLE", func() error {
		var e error
		account, p, credentials, e = s.account(owner, r.AccountID)
		if e != nil {
			return e
		}
		var ok bool
		manager, ok = p.(checkFileManager)
		if !ok {
			return errors.New("此网盘暂不支持功能检查")
		}
		ctx = s.credentialContext(ctx, owner, &account)
		ctx = context.WithValue(ctx, baiduRunKey{}, &baiduRunSession{})
		identity, e := p.Validate(ctx, credentials)
		if e == nil && account.ProviderUserID != "" && identity.ID != account.ProviderUserID {
			e = errors.New("网盘身份与原账号不符")
		}
		r.AuthStatus = "unknown"
		if e == nil {
			r.AuthStatus = "valid"
		} else if errors.Is(e, ErrAuthExpired) {
			r.AuthStatus = "expired"
		}
		saveErr := s.store.update(owner, func(st *userState) error {
			a, ok := st.Accounts[r.AccountID]
			if !ok {
				return ErrNotFound
			}
			a.AuthCheckedAt = now()
			if r.AuthStatus == "unknown" {
				a.AuthCheckedAt = 0
			}
			a.UpdatedAt = now()
			if e == nil {
				a.Status = "ready"
				a.LastError = ""
			} else if errors.Is(e, ErrAuthExpired) {
				a.Status = "expired"
				a.LastError = safeError(e, credentials)
			}
			st.Accounts[a.ID] = a
			return nil
		})
		if saveErr != nil {
			return saveErr
		}
		return e
	})
	stage(2, "TARGET_PREPARE_FAILED", func() error {
		var e error
		share, e = p.ReadShare(ctx, credentials, ShareLink{URL: source.URL, Password: source.Password})
		if e != nil {
			return e
		}
		if account.Provider == "baidu" && account.ProviderUserID != "" && share.Data["owner"] == account.ProviderUserID {
			return &functionFailure{"SOURCE_UNAVAILABLE", "测试分享属于当前百度账号，请换用另一账号分享的小文件以检查转存能力"}
		}
		entry, e = selectCheckFile(share.Entries, source.MaxSize)
		if e != nil {
			return e
		}
		r.TargetName = checkFolderPrefix + strings.ToLower(r.ID)
		files, e := manager.checkFiles(ctx, credentials, p.RootID())
		if e != nil {
			return e
		}
		for _, f := range files {
			if f.Name == r.TargetName {
				return errors.New("测试目录名称已存在，未写入")
			}
		}
		// Persist intent before any remote write. An interrupted create is never adopted.
		r.CleanupPending = true
		if e = s.saveFunction(owner, r); e != nil {
			return e
		}
		folder, e := p.CreateFolder(ctx, credentials, p.RootID(), r.TargetName)
		if e != nil {
			return e
		}
		r.TargetID = folder.ID
		// Record the native create receipt before waiting for list consistency.
		if r.Provider != "baidu" {
			r.TargetObject = checkObject{ID: folder.ID, Name: r.TargetName, IsDir: true}
		}
		if e = s.saveFunction(owner, r); e != nil {
			return e
		}
		confirmedFolder, e := waitCheckFolder(ctx, credentials, manager, p.RootID(), r.TargetName, r.TargetID, r.Provider == "baidu")
		if e != nil {
			return e
		}
		r.TargetObject = confirmedFolder
		if e = s.saveFunction(owner, r); e != nil {
			return e
		}
		contents, e := manager.checkFiles(ctx, credentials, r.TargetID)
		if e != nil {
			return e
		}
		if len(contents) != 0 {
			return errors.New("测试目录不是空目录，已停止写入")
		}
		return nil
	})
	stage(3, "TRANSFER_UNCONFIRMED", func() error {
		r.Submitted = true
		if e := s.saveFunction(owner, r); e != nil {
			return e
		}
		if batch, ok := p.(BatchSaver); ok {
			result, e := batch.SaveBatch(ctx, credentials, share, []Entry{entry}, r.TargetID)
			r.Submitted = result.Submitted
			if e != nil {
				return e
			}
			if len(result.Confirmed) != 1 || result.Confirmed[0] != entry.ID {
				return errors.New("转存结果尚未确认，保留测试目录供检查")
			}
		} else {
			if e := p.Save(ctx, credentials, share, entry, r.TargetID); e != nil {
				return e
			}
		}
		r.TransferConfirmed = true
		return nil
	})
	stage(4, "TRANSFER_UNCONFIRMED", func() error {
		files, e := manager.checkFiles(ctx, credentials, r.TargetID)
		if e != nil {
			return e
		}
		if len(files) != 1 || files[0].IsDir || files[0].Name != entry.Name || files[0].Size != entry.Size || files[0].ID == "" {
			return errors.New("测试目录中的文件与本次转存不一致，未执行删除")
		}
		r.SavedObject = files[0]
		return nil
	})
	stage(5, "DELETE_FAILED", func() error { return s.deleteFunctionObjects(ctx, owner, credentials, p, manager, &r) })
	stage(6, "CLEANUP_UNCONFIRMED", func() error {
		if e := waitCheckAbsent(ctx, credentials, manager, p.RootID(), r.TargetObject); e != nil {
			return e
		}
		r.CleanupPending = false
		return nil
	})
	r.Status = "passed"
	if failedStage >= 0 {
		r.Status = "failed"
		if r.CleanupPending {
			r.Status = "incomplete"
		}
	}
	r.FinishedAt = now()
	_ = s.store.update(owner, func(st *userState) error { finishFunction(st, r); return nil })
}

// A successful create can precede the provider's directory listing update.
func waitCheckFolder(ctx context.Context, c Credentials, m checkFileManager, root, name, id string, pathID bool) (checkObject, error) {
	for attempt := 0; attempt < 20; attempt++ {
		files, err := m.checkFiles(ctx, c, root)
		if err != nil {
			return checkObject{}, err
		}
		var found checkObject
		count := 0
		for _, f := range files {
			if f.Name == name || (!pathID && f.ID == id) {
				if !f.IsDir || f.Name != name || (!pathID && f.ID != id) {
					return checkObject{}, errors.New("测试目录身份不一致")
				}
				found = f
				count++
			}
		}
		if count == 1 && found.ID != "" {
			return found, nil
		}
		if count > 1 {
			return checkObject{}, errors.New("测试目录名称不唯一")
		}
		if err := bxWait(ctx, 500*time.Millisecond); err != nil {
			return checkObject{}, err
		}
	}
	return checkObject{}, errors.New("无法确认新建测试目录")
}

func sameCheckObject(a, b checkObject) bool {
	return a.ID == b.ID && a.Name == b.Name && a.IsDir == b.IsDir && (!a.IsDir && a.Size == b.Size || a.IsDir)
}
func waitCheckAbsent(ctx context.Context, c Credentials, m checkFileManager, parent string, obj checkObject) error {
	for i := 0; i < 20; i++ {
		items, err := m.checkFiles(ctx, c, parent)
		if err != nil {
			return err
		}
		found := false
		for _, f := range items {
			if f.ID == obj.ID || f.Name == obj.Name {
				found = true
			}
		}
		if !found {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return errors.New("删除后仍能看到测试内容，清理尚未确认")
}
func (s *Service) deleteFunctionObjects(ctx context.Context, owner string, c Credentials, p Provider, m checkFileManager, r *functionRecord) error {
	if r.TargetID == "" || r.TargetID == p.RootID() || r.TargetObject.ID == "" || !r.TargetObject.IsDir || r.TargetObject.Name != checkFolderPrefix+strings.ToLower(r.ID) {
		return errors.New("测试目录归属不明确，不自动删除")
	}
	root, err := m.checkFiles(ctx, c, p.RootID())
	if err != nil {
		return err
	}
	found := false
	for _, f := range root {
		if f.ID == r.TargetObject.ID || f.Name == r.TargetName {
			if !sameCheckObject(f, r.TargetObject) {
				return errors.New("测试目录身份发生变化，停止清理")
			}
			found = true
		}
	}
	if !found {
		if r.Submitted && !r.DeleteSubmitted {
			return errors.New("测试内容在本次删除前已消失，无法确认删除能力")
		}
		return nil
	}
	files, err := m.checkFiles(ctx, c, r.TargetID)
	if err != nil {
		return err
	}
	for _, f := range files {
		if r.SavedObject.ID == "" || !sameCheckObject(f, r.SavedObject) || f.IsDir {
			return errors.New("测试目录中存在非本次确认的文件，不自动删除")
		}
	}
	if len(files) == 0 && r.Submitted && !r.DeleteSubmitted {
		return errors.New("测试文件在删除前已消失，无法确认删除能力")
	}
	if len(files) > 1 {
		return errors.New("测试目录内容异常，不自动删除")
	}
	if len(files) == 1 {
		r.DeleteSubmitted = true
		if err = s.saveFunction(owner, *r); err != nil {
			return err
		}
		if err = m.deleteCheckObject(ctx, c, r.TargetID, r.SavedObject); err != nil {
			return err
		}
		if err = waitCheckAbsent(ctx, c, m, r.TargetID, r.SavedObject); err != nil {
			return err
		}
	}
	// Re-read immediately before removing the empty, journaled folder.
	files, err = m.checkFiles(ctx, c, r.TargetID)
	if err != nil {
		return err
	}
	if len(files) != 0 {
		return errors.New("测试目录仍有内容，不删除目录")
	}
	return m.deleteCheckObject(ctx, c, p.RootID(), r.TargetObject)
}
func (s *Service) QueueFunctionCleanup(owner, id string) error {
	err := s.store.update(owner, func(st *userState) error {
		r, ok := st.Checks[id]
		if !ok {
			return ErrNotFound
		}
		if active(r.Status) || r.CleanupRequested || r.CleanupStatus == "running" {
			return ErrBusy
		}
		if !r.CleanupPending {
			return errors.New("该检查没有待清理内容")
		}
		// Compatibility for the first live 1.5.14 diagnostic: the persisted,
		// fixed provider error proves this transfer request was rejected.
		if r.Provider == "baidu" && r.Error == "百度网盘操作失败（错误码 2）" && len(r.Stages) == 7 && r.Stages[3].Status == "failed" {
			r.Submitted = false
		}
		// A possibly still running transfer cannot be cleaned safely without a provider task handle.
		if r.Submitted && (!r.TransferConfirmed || r.SavedObject.ID == "") {
			return errors.New("转存结果不确定，请先在网盘确认测试目录；不会自动重存或删除未确认文件")
		}
		if r.TargetObject.ID == "" {
			if r.TargetID == "" || r.Provider == "baidu" || r.Submitted || r.TargetName != checkFolderPrefix+strings.ToLower(r.ID) {
				return errors.New("目录创建结果不确定，请在网盘检查记录中的测试目录")
			}
			// Older checks persisted the native create ID before a delayed list
			// failed. Cleanup still checks exact identity and empty contents.
			r.TargetObject = checkObject{ID: r.TargetID, Name: r.TargetName, IsDir: true}
		}
		r.CleanupRequested = true
		r.CleanupStatus = "queued"
		st.Checks[id] = r
		return nil
	})
	if err == nil {
		s.notify()
	}
	return err
}
func (s *Service) executeCleanup(owner string, r functionRecord) {
	ctx, cancel := context.WithTimeout(s.ctx, 90*time.Second)
	defer cancel()
	unlock, err := s.lockAccount(ctx, owner, r.AccountID)
	if err != nil {
		return
	}
	defer unlock()
	r.CleanupRequested = false
	r.CleanupStatus = "running"
	if s.saveFunction(owner, r) != nil {
		return
	}
	start := now()
	a, p, c, err := s.account(owner, r.AccountID)
	if err == nil {
		ctx = s.credentialContext(ctx, owner, &a)
		ctx = context.WithValue(ctx, baiduRunKey{}, &baiduRunSession{})
		m, ok := p.(checkFileManager)
		if !ok {
			err = errors.New("网盘不支持清理")
		} else {
			err = s.deleteFunctionObjects(ctx, owner, c, p, m, &r)
			if err == nil {
				err = waitCheckAbsent(ctx, c, m, p.RootID(), r.TargetObject)
			}
		}
	}
	result := "passed"
	var code any
	r.CleanupStatus = "passed"
	if err != nil {
		result = "failed"
		code = "CLEANUP_UNCONFIRMED"
		r.CleanupStatus = "failed"
		r.Error = safeError(err, c)
	} else {
		r.CleanupPending = false
	}
	_ = s.store.update(owner, func(st *userState) error {
		st.Checks[r.ID] = r
		enqueueCenterEvent(st, "account_check_cleanup_finished", map[string]any{"run_id": r.ID, "result": result, "duration_ms": now() - start, "error_code": code, "deletion_mode": checkDeletionMode(r.Provider)})
		return nil
	})
}
func recoverFunctions(st *userState) {
	for id, r := range st.Checks {
		if r.Status == "queued" && r.Trigger != "manual" && r.ScheduledFor < midnight(time.Now()).UnixMilli() {
			r.Status = "skipped"
			r.Error = "离线期间旧计划已跳过，仅补最近一次"
			r.ErrorCode = "CLIENT_INTERRUPTED"
			r.StartedAt = now()
			r.FinishedAt = now()
			finishFunction(st, r)
			continue
		}
		if r.CleanupStatus == "running" {
			r.CleanupStatus = "failed"
			r.Error = "清理被服务重启中断，请重试清理"
			st.Checks[id] = r
		}
		if r.Status != "running" {
			continue
		}
		r.Status = "incomplete"
		r.Error = "服务重启中断检查，未重复转存；请查看测试目录状态"
		r.ErrorCode = "CLIENT_INTERRUPTED"
		r.FinishedAt = now()
		finishFunction(st, r)
	}
}

// FunctionSourcePreview reads only the configured public share. It returns no
// credentials, provider user IDs, share tokens, or arbitrary account file lists.
func (s *Service) FunctionSourcePreview(ctx context.Context, owner, accountID string) (map[string]any, error) {
	unlock, err := s.lockAccount(ctx, owner, accountID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	a, p, c, err := s.account(owner, accountID)
	if err != nil {
		return nil, err
	}
	src, err := s.fetchCheckSource(ctx, a.Provider)
	if err != nil {
		return nil, err
	}
	ctx = s.credentialContext(ctx, owner, &a)
	ctx = context.WithValue(ctx, baiduRunKey{}, &baiduRunSession{})
	share, err := p.ReadShare(ctx, c, ShareLink{URL: src.URL, Password: src.Password})
	if err != nil {
		return nil, errors.New(safeError(err, c))
	}
	files := []map[string]any{}
	for _, e := range share.Entries {
		if e.IsDir {
			continue
		}
		if len(files) < 20 {
			files = append(files, map[string]any{"path": e.Path, "size": e.Size})
		}
	}
	selected, selectionErr := selectCheckFile(share.Entries, src.MaxSize)
	sameOwner := a.Provider == "baidu" && a.ProviderUserID != "" && share.Data["owner"] == a.ProviderUserID
	result := map[string]any{"provider": a.Provider, "selected_path": selected.Path, "selected_size": selected.Size, "can_check": selectionErr == nil && !sameOwner, "same_owner": sameOwner, "files": files}
	if selectionErr != nil {
		result["error"] = selectionErr.Error()
	}
	return result, nil
}
