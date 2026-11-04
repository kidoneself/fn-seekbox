package cloud

import (
	"context"
	"errors"
	"fmt"
	"path"
)

// A bounded application batch, not a claimed upstream maximum.
const transferBatchSize = 100

func batchEntries(entries []Entry) ([]string, map[string]bool, error) {
	if len(entries) == 0 || len(entries) > transferBatchSize {
		return nil, nil, errors.New("转存批次数量无效")
	}
	ids := make([]string, 0, len(entries))
	names := map[string]bool{}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir || e.ID == "" || !bxName(e.Name) || seen[e.ID] || names[e.Name] || e.ParentID != entries[0].ParentID || path.Dir(e.Path) != path.Dir(entries[0].Path) {
			return nil, nil, errors.New("转存批次必须是同目录且不重复的文件")
		}
		ids = append(ids, e.ID)
		seen[e.ID] = true
		names[e.Name] = true
	}
	return ids, names, nil
}

func transferMark(owner string, job jobRecord, share Share, entry Entry) string {
	return digest(owner, job.AccountID, share.ID, job.Request.DestinationID, entry.ID, entry.Path, entry.Revision, fmt.Sprint(entry.Size))
}

func (s *Service) saveBatch(ctx context.Context, owner string, job *jobRecord, p Provider, credentials Credentials, share Share, entries []Entry, destination string) error {
	if err := s.canContinue(ctx, owner, *job); err != nil {
		return err
	}
	marks := map[string]string{}
	byID := map[string]string{}
	for _, e := range entries {
		key := transferMark(owner, *job, share, e)
		marks[key] = "pending"
		byID[e.ID] = key
	}
	if err := s.store.saveJobMarks(owner, *job, marks); err != nil {
		return err
	}
	var result BatchResult
	var err error
	if batch, ok := p.(BatchSaver); ok {
		result, err = batch.SaveBatch(ctx, credentials, share, entries, destination)
	} else {
		// Compatibility for providers without a batch implementation; caller uses one item.
		result.Submitted = true
		err = p.Save(ctx, credentials, share, entries[0], destination)
		if err == nil {
			result.Confirmed = []string{entries[0].ID}
		} else if errors.Is(err, ErrDestinationExists) {
			result.Submitted = false
		}
	}
	confirmed := map[string]bool{}
	for _, id := range result.Confirmed {
		if byID[id] == "" || confirmed[id] || !result.Submitted {
			return errors.New("网盘返回了无效的批次确认结果，请核对网盘")
		}
		confirmed[id] = true
	}
	updates := map[string]string{}
	for id, key := range byID {
		if confirmed[id] {
			updates[key] = "done"
		} else if !result.Submitted {
			updates[key] = ""
		}
	}
	next := *job
	next.Saved += len(confirmed)
	if saveErr := s.store.saveJobMarks(owner, next, updates); saveErr != nil {
		return errors.New("网盘转存后本地进度写入失败，请先核对结果")
	}
	*job = next
	if err != nil {
		if !result.Submitted {
			return err
		}
		return fmt.Errorf("本批 %d 个文件已确认 %d 个，其余结果未确认，已停止自动重试，请到网盘核对：%w", len(entries), len(confirmed), err)
	}
	if len(confirmed) != len(entries) {
		return errors.New("网盘没有确认本批全部文件，已停止自动重试，请到网盘核对")
	}
	return nil
}
