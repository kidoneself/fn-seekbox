package cloud

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	bolt "go.etcd.io/bbolt"
	"pansou/util/json"
)

var updateEventsBucket = []byte("update_events")
var updateMetadataBucket = []byte("update_metadata")

// ReleaseEvent intentionally contains no account names, credentials or file data.
type ReleaseEvent struct {
	ReleaseID   string    `json:"release_id"`
	EventID     string    `json:"event_id"`
	Version     string    `json:"version"`
	FromVersion string    `json:"from_version"`
	Status      string    `json:"status"`
	ErrorCode   string    `json:"error_code,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
}

// EnqueueReleaseEvent durably takes ownership of an updater event. The update
// helper must keep its own event file if this transaction fails.
func (s *Service) EnqueueReleaseEvent(event ReleaseEvent) error {
	if event.ReleaseID == "" || len(event.ReleaseID) > 128 || event.EventID == "" || len(event.EventID) > 128 || len(event.Version) > 80 || len(event.FromVersion) > 80 || len(event.ErrorCode) > 80 || event.OccurredAt.IsZero() {
		return errors.New("更新报告格式无效")
	}
	if event.Status != "success" && event.Status != "failed" && event.Status != "rolled_back" {
		return errors.New("更新报告状态无效")
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return s.store.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(updateEventsBucket)
		if err != nil {
			return err
		}
		old := b.Get([]byte(event.EventID))
		if old != nil {
			if string(old) != string(data) {
				return errors.New("更新报告标识冲突")
			}
			return nil
		}
		return b.Put([]byte(event.EventID), data)
	})
}

// Existing center registrations are per fnOS owner. Choose one registered,
// reporting-enabled owner exactly once for machine-wide release receipts. This
// prevents a retry from being counted as a second installation under another
// user. If that owner's reporting is disabled, receipts stay local until enabled.
func (s *Service) updateReportOwner() (string, error) {
	var chosen string
	err := s.store.db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket(updateMetadataBucket); b != nil {
			chosen = string(b.Get([]byte("report_owner")))
		}
		return nil
	})
	if err != nil || chosen != "" {
		return chosen, err
	}
	owners, err := s.store.owners()
	if err != nil {
		return "", err
	}
	sort.Strings(owners)
	for _, owner := range owners {
		state, err := s.store.read(owner)
		if err != nil {
			return "", err
		}
		if !state.Center.Config.ReportingEnabled {
			continue
		}
		secret, err := s.centerSecret(owner, state.Center)
		if err != nil {
			return "", err
		}
		if secret["report_token"] == "" {
			continue
		}
		err = s.store.db.Update(func(tx *bolt.Tx) error {
			b, err := tx.CreateBucketIfNotExists(updateMetadataBucket)
			if err != nil {
				return err
			}
			chosen = string(b.Get([]byte("report_owner")))
			if chosen != "" {
				return nil
			}
			chosen = owner
			return b.Put([]byte("report_owner"), []byte(owner))
		})
		return chosen, err
	}
	return "", nil
}
func (s *Service) syncReleaseReports() {
	release, err := s.beginOperation()
	if err != nil {
		return
	}
	defer release()
	s.centerMu.Lock()
	defer s.centerMu.Unlock()
	var events []ReleaseEvent
	err = s.store.db.View(func(tx *bolt.Tx) error {
		if metadata := tx.Bucket(updateMetadataBucket); metadata != nil {
			retryAt, _ := strconv.ParseInt(string(metadata.Get([]byte("report_retry_at"))), 10, 64)
			if now() < retryAt {
				return nil
			}
		}
		b := tx.Bucket(updateEventsBucket)
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil && len(events) < 20; k, v = c.Next() {
			var event ReleaseEvent
			if err := json.Unmarshal(v, &event); err != nil {
				return err
			}
			events = append(events, event)
		}
		return nil
	})
	if err != nil || len(events) == 0 {
		return
	}
	owner, err := s.updateReportOwner()
	if err != nil || owner == "" {
		return
	}
	state, err := s.store.read(owner)
	if err != nil || !state.Center.Config.ReportingEnabled {
		return
	}
	secret, err := s.centerSecret(owner, state.Center)
	if err != nil || secret["report_token"] == "" {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 90*time.Second)
	defer cancel()
	for _, event := range events {
		var receipt struct {
			EventID  string `json:"event_id"`
			Accepted bool   `json:"accepted"`
		}
		if err := s.centerRequest(ctx, "POST", "/api/client/releases/report", secret["report_token"], event, &receipt); err != nil {
			delay := time.Minute
			var remoteErr *centerError
			if errors.As(err, &remoteErr) && remoteErr.RetryAfter > delay {
				delay = remoteErr.RetryAfter
			}
			_ = s.store.db.Update(func(tx *bolt.Tx) error {
				b, e := tx.CreateBucketIfNotExists(updateMetadataBucket)
				if e != nil {
					return e
				}
				return b.Put([]byte("report_retry_at"), []byte(strconv.FormatInt(now()+delay.Milliseconds(), 10)))
			})
			return
		}
		if receipt.EventID != event.EventID || !receipt.Accepted {
			return
		}
		if err := s.store.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(updateEventsBucket).Delete([]byte(event.EventID)) }); err != nil {
			return
		}
	}
}
