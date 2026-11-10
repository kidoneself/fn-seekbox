package main

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	bolt "go.etcd.io/bbolt"
	"sort"
	"time"
)

func (a *app) listSlots(c *gin.Context) {
	q, ok := a.query(c, "slots")
	if !ok {
		return
	}
	items := []gin.H{}
	more := false
	last := ""
	err := a.store.db.View(func(tx *bolt.Tx) error {
		insts := filteredInstances(tx, q)
		runs := map[string][]Run{}
		if err := walk[Run](tx, "runs", func(_ string, r Run) error {
			if r.Trigger != "manual" && r.ScheduledFor != nil && within(*r.ScheduledFor, q) {
				runs[runSlot(r)] = append(runs[runSlot(r)], r)
			}
			return nil
		}); err != nil {
			return err
		}
		return walk[Slot](tx, "slots", func(k string, s Slot) error {
			if more || k <= q.After || !within(s.ScheduledFor, q) || q.Provider != "" && s.Provider != q.Provider {
				return nil
			}
			if _, ok := insts[s.InstallationID]; !ok {
				return nil
			}
			if len(items) == q.Limit {
				more = true
				return nil
			}
			rs := runs[k]
			sort.Slice(rs, func(i, j int) bool { return runEvidenceTime(rs[i]).Before(runEvidenceTime(rs[j])) })
			status := "unreported"
			first, latest := "", ""
			passed, complete := false, false
			for _, r := range rs {
				if first == "" && r.OriginalResult != "" {
					first = r.OriginalResult
				}
				if r.OriginalResult != "" {
					latest = r.OriginalResult
					complete = true
				}
				passed = passed || r.CycleCompleted
			}
			if len(rs) > 0 {
				status = "started"
			}
			if complete {
				status = latest
			}
			if passed {
				status = "passed"
			}
			items = append(items, gin.H{"installation_id": s.InstallationID, "account_ref": s.AccountRef, "provider": s.Provider, "scheduled_for": s.ScheduledFor, "estimated": true, "attempt_count": len(rs), "first_result": first, "latest_result": latest, "status": status, "cycle_completed": passed, "delayed": len(rs) == 0 && q.AsOf.After(s.ScheduledFor.Add(10*time.Minute))})
			last = k
			return nil
		})
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	var next any
	if more {
		raw, _ := json.Marshal(pageCursor{last, q.AsOf, q.Fingerprint})
		next = a.signCursor(raw)
	}
	reply(c, 200, 0, "ok", gin.H{"items": items, "next_cursor": next, "as_of": q.AsOf, "time_basis": "scheduled_for", "estimated": true})
}
