package cloud

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

func (s *Service) run() {
	defer s.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if s.ctx.Err() != nil {
			return
		}
		s.schedule()
		owner, job, ok := s.nextJob()
		if ok {
			s.execute(owner, job)
			continue
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

func (s *Service) schedule() {
	owners, err := s.store.owners()
	if err != nil {
		return
	}
	for _, owner := range owners {
		_ = s.store.update(owner, func(state *userState) error {
			for id, sub := range state.Subscriptions {
				if !sub.Enabled || sub.NextCheckAt > now() {
					continue
				}
				busy := false
				for _, job := range state.Jobs {
					if job.SubscriptionID == id && active(job.Status) {
						busy = true
						break
					}
				}
				if busy {
					continue
				}
				a, ok := state.Accounts[sub.AccountID]
				if !ok || a.Status != "ready" {
					sub.Status, sub.LastError = "error", "请重新连接或检查网盘账号"
					sub.NextCheckAt = now() + int64(sub.IntervalMinutes)*60000
					state.Subscriptions[id] = sub
					continue
				}
				if _, err := addJob(state, sub.TransferRequest, sub.Provider, id); err != nil {
					continue
				}
				sub.Status = "running"
				state.Subscriptions[id] = sub
			}
			return nil
		})
	}
}

func (s *Service) nextJob() (string, jobRecord, bool) {
	owners, err := s.store.owners()
	if err != nil {
		return "", jobRecord{}, false
	}
	var selected jobRecord
	selectedOwner := ""
	for _, owner := range owners {
		state, err := s.store.read(owner)
		if err != nil {
			continue
		}
		for _, job := range state.Jobs {
			if job.Status == "queued" && (selectedOwner == "" || job.CreatedAt < selected.CreatedAt) {
				selected, selectedOwner = job, owner
			}
		}
	}
	return selectedOwner, selected, selectedOwner != ""
}

func (s *Service) execute(owner string, job jobRecord) {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Minute)
	defer cancel()
	unlock, lockErr := s.lockAccount(ctx, owner, job.AccountID)
	if lockErr != nil {
		return
	}
	defer unlock()
	job.Status = "running"
	if err := s.saveJob(owner, job); err != nil {
		return
	}
	credentials := Credentials{}
	err := func() error {
		a, p, creds, err := s.account(owner, job.AccountID)
		if err != nil {
			return err
		}
		credentials = creds
		ctx = s.credentialContext(ctx, owner, &a)
		ctx = context.WithValue(ctx, baiduRunKey{}, &baiduRunSession{})
		if a.Status != "ready" {
			return ErrAuthExpired
		}
		if err = s.canContinue(ctx, owner, job); err != nil {
			return err
		}
		if err = verifyDestination(ctx, p, credentials, job.Request); err != nil {
			return err
		}
		share, err := p.ReadShare(ctx, credentials, ShareLink{URL: job.Request.ShareURL, Password: job.Request.Password})
		if err != nil {
			return err
		}
		if share.ID == "" || len(share.Entries) > 10000 {
			return errors.New("分享内容数量超出限制或返回不完整")
		}
		paths := map[string]bool{}
		for _, entry := range share.Entries {
			if !validEntry(entry) || paths[entry.Path] {
				return errors.New("分享包含不安全或重复的文件路径，已停止转存")
			}
			paths[entry.Path] = true
			if !entry.IsDir {
				job.Total++
			}
		}
		if err = s.saveJob(owner, job); err != nil {
			return err
		}
		if job.Request.FilterAds {
			share.Entries, job.Filtered = filterAdAttachments(share.Entries)
			if err = s.saveJob(owner, job); err != nil {
				return err
			}
		}
		sort.SliceStable(share.Entries, func(i, j int) bool { return share.Entries[i].Path < share.Entries[j].Path })
		directories := map[string]string{".": job.Request.DestinationID}
		var ensureDirectory func(string) (string, error)
		ensureDirectory = func(relative string) (string, error) {
			if id, ok := directories[relative]; ok {
				return id, nil
			}
			if err := s.canContinue(ctx, owner, job); err != nil {
				return "", err
			}
			parentID, err := ensureDirectory(path.Dir(relative))
			if err != nil {
				return "", err
			}
			name := path.Base(relative)
			folders, err := p.ListFolders(ctx, credentials, parentID)
			if err != nil {
				return "", err
			}
			found := ""
			matched := false
			for _, folder := range folders {
				if folder.Name == name {
					if matched {
						return "", errors.New("目标目录存在多个同名文件夹，请先整理")
					}
					found, matched = folder.ID, true
				}
			}
			if !matched {
				folder, err := p.CreateFolder(ctx, credentials, parentID, name)
				if err != nil {
					return "", err
				}
				found = folder.ID
			}
			if found == "" {
				return "", errors.New("网盘未返回保存目录标识")
			}
			directories[relative] = found
			return found, nil
		}

		groups := map[string][]Entry{}
		parents := []string{}
		for _, entry := range share.Entries {
			if err = s.canContinue(ctx, owner, job); err != nil {
				return err
			}
			if entry.IsDir {
				if _, err = ensureDirectory(entry.Path); err != nil {
					return err
				}
				continue
			}
			mark, err := s.store.mark(transferMark(owner, job, share, entry))
			if err != nil {
				return err
			}
			if mark == "done" {
				job.Skipped++
				continue
			}
			if mark == "pending" {
				return fmt.Errorf("文件 %s 的上次转存结果未确认，已停止自动重试；请到网盘核对，必要时改用其他保存目录", entry.Name)
			}
			parent := path.Dir(entry.Path)
			if _, ok := groups[parent]; !ok {
				parents = append(parents, parent)
			}
			groups[parent] = append(groups[parent], entry)
		}
		if err = s.saveJob(owner, job); err != nil {
			return err
		}
		limit := 1
		if _, ok := p.(BatchSaver); ok {
			limit = transferBatchSize
		}
		for _, parent := range parents {
			destination, err := ensureDirectory(parent)
			if err != nil {
				return err
			}
			entries := groups[parent]
			for start := 0; start < len(entries); start += limit {
				end := start + limit
				if end > len(entries) {
					end = len(entries)
				}
				if err = s.saveBatch(ctx, owner, &job, p, credentials, share, entries[start:end], destination); err != nil {
					return err
				}
			}
		}

		return nil
	}()
	job.FinishedAt = now()
	job.Status = "succeeded"
	if err != nil {
		job.Status = "failed"
		if job.Saved > 0 {
			job.Status = "partial"
		}
		if errors.Is(err, context.Canceled) {
			job.Status = "interrupted"
		}
		job.Error = safeError(err, credentials)
	}
	_ = s.store.update(owner, func(state *userState) error {
		state.Jobs[job.ID] = job
		if sub, ok := state.Subscriptions[job.SubscriptionID]; ok {
			sub.LastCheckedAt = job.FinishedAt
			sub.NextCheckAt = job.FinishedAt + int64(sub.IntervalMinutes)*60000
			sub.Transferred += job.Saved
			sub.LastError = job.Error
			sub.Status = "idle"
			if err != nil {
				sub.Status = "error"
			}
			if !sub.Enabled {
				sub.Status = "paused"
			}
			state.Subscriptions[sub.ID] = sub
		}
		if errors.Is(err, ErrAuthExpired) {
			if a, ok := state.Accounts[job.AccountID]; ok {
				a.Status = "expired"
				a.LastError = ErrAuthExpired.Error()
				a.UpdatedAt = now()
				state.Accounts[a.ID] = a
			}
		}
		return nil
	})
}

// As in the existing media app, walk the directory path from the drive root.
// An empty listing for a deleted opaque ID alone does not prove it still exists.
func verifyDestination(ctx context.Context, p Provider, credentials Credentials, req TransferRequest) error {
	if req.DestinationName == "" {
		if req.DestinationID == p.RootID() {
			return nil
		}
		return errors.New("保存目录缺少路径，请重新选择目录")
	}
	parent := p.RootID()
	if req.DestinationName != "/" {
		for _, segment := range strings.Split(strings.TrimPrefix(req.DestinationName, "/"), "/") {
			folders, err := p.ListFolders(ctx, credentials, parent)
			if err != nil {
				return err
			}
			found := ""
			matches := 0
			for _, folder := range folders {
				if folder.Name == segment {
					found = folder.ID
					matches++
				}
			}
			if matches != 1 {
				return errors.New("保存目录已删除或存在同名目录，请重新选择保存位置")
			}
			parent = found
		}
	}
	if parent != req.DestinationID {
		return errors.New("保存路径已指向其他目录，已停止转存，请重新选择保存位置")
	}
	return nil
}

func (s *Service) saveJob(owner string, job jobRecord) error {
	return s.store.update(owner, func(state *userState) error {
		if _, ok := state.Jobs[job.ID]; !ok {
			return ErrNotFound
		}
		state.Jobs[job.ID] = job
		return nil
	})
}

func (s *Service) canContinue(ctx context.Context, owner string, job jobRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := s.store.read(owner)
	if err != nil {
		return err
	}
	if _, ok := state.Accounts[job.AccountID]; !ok {
		return ErrNotFound
	}
	if job.SubscriptionID != "" {
		sub, ok := state.Subscriptions[job.SubscriptionID]
		if !ok || !sub.Enabled {
			return errors.New("追更已暂停，当前正在进行的网盘请求结束后停止")
		}
	}
	return nil
}
