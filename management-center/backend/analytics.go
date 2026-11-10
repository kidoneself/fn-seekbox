package main

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	bolt "go.etcd.io/bbolt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Query struct {
	From, To, AsOf                                                                                              time.Time
	Provider, Version, Instance, Source, Result, Error, Trigger, Metric, Group, Granularity, After, Fingerprint string
	Limit                                                                                                       int
}
type pageCursor struct {
	After       string    `json:"after"`
	AsOf        time.Time `json:"as_of"`
	Fingerprint string    `json:"fingerprint"`
}

func (a *app) query(c *gin.Context, kind string) (Query, bool) {
	q := Query{AsOf: a.now(), Limit: 20, Granularity: "day"}
	v, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		fail(c, 400, "INVALID_QUERY", "查询参数无法解析")
		return q, false
	}
	allowed := []string{"from", "to", "timezone", "provider", "app_version", "installation_id"}
	switch kind {
	case "overview":
		allowed = append(allowed, "source_id", "trigger")
	case "trends":
		allowed = append(allowed, "source_id", "trigger", "metric", "granularity", "group_by")
	case "slots":
		allowed = append(allowed, "limit", "cursor")
	case "checks", "events":
		allowed = append(allowed, "source_id", "result", "error_code", "trigger", "limit", "cursor")
	case "instances", "accounts":
		allowed = []string{"timezone", "provider", "app_version", "installation_id", "limit", "cursor"}
	case "audit":
		allowed = []string{"from", "to", "timezone", "limit", "cursor", "actor", "endpoint_id", "status"}
	}
	for k, values := range v {
		if !oneOf(k, allowed...) || len(values) != 1 || values[0] == "" {
			fail(c, 400, "INVALID_QUERY", "不支持、重复或为空的查询参数："+k)
			return q, false
		}
	}
	if z := v.Get("timezone"); z != "" && z != "Asia/Shanghai" {
		fail(c, 400, "INVALID_TIMEZONE", "仅支持 Asia/Shanghai")
		return q, false
	}
	if kind == "audit" && v.Get("status") != "" {
		n, e := strconv.Atoi(v.Get("status"))
		if e != nil || n < 100 || n > 599 {
			fail(c, 400, "INVALID_QUERY", "status 必须为有效HTTP状态码")
			return q, false
		}
	}
	q.Provider = v.Get("provider")
	q.Version = v.Get("app_version")
	q.Instance = v.Get("installation_id")
	q.Source = v.Get("source_id")
	q.Trigger = v.Get("trigger")
	q.Result = v.Get("result")
	q.Error = v.Get("error_code")
	q.Group = v.Get("group_by")
	q.Metric = v.Get("metric")
	if q.Provider != "" && !validProvider(q.Provider) || q.Version != "" && !validVersion(q.Version) || q.Instance != "" && !validID(q.Instance) || q.Source != "" && !validID(q.Source) || !oneOf(q.Trigger, "", "manual", "daily", "catch_up") || !oneOf(q.Result, "", "passed", "failed", "incomplete", "skipped") || q.Error != "" && !allowedError(&q.Error) || !oneOf(q.Group, "", "provider", "source_id", "app_version", "result", "error_code") {
		fail(c, 400, "INVALID_QUERY", "筛选值不合法")
		return q, false
	}
	if s := v.Get("granularity"); s != "" {
		q.Granularity = s
	}
	if !oneOf(q.Granularity, "day", "hour") {
		fail(c, 400, "INVALID_QUERY", "granularity 仅支持 day/hour")
		return q, false
	}
	if s := v.Get("limit"); s != "" {
		q.Limit, err = strconv.Atoi(s)
		if err != nil || q.Limit < 1 || q.Limit > 100 {
			fail(c, 400, "INVALID_QUERY", "limit 需要介于1–100")
			return q, false
		}
	}
	vv := url.Values{}
	for k, x := range v {
		if k != "cursor" && k != "limit" {
			vv[k] = x
		}
	}
	q.Fingerprint = digest(kind + "?" + vv.Encode())
	if s := v.Get("cursor"); s != "" {
		raw, e := a.verifyCursor(s)
		var cur pageCursor
		if e != nil || json.Unmarshal(raw, &cur) != nil || cur.Fingerprint != q.Fingerprint {
			fail(c, 400, "INVALID_CURSOR", "游标无效或查询条件已改变")
			return q, false
		}
		q.AsOf = cur.AsOf
		q.After = cur.After
	}
	q.From = midnight(q.AsOf)
	q.To = q.From.AddDate(0, 0, 1)
	if kind != "overview" {
		q.From = q.From.AddDate(0, 0, -6)
	}
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"from", &q.From}, {"to", &q.To}} {
		if s := v.Get(p.name); s != "" {
			*p.dst, err = time.Parse(time.RFC3339, s)
			if err != nil {
				fail(c, 400, "INVALID_QUERY", "时间必须为带时区的 RFC3339")
				return q, false
			}
		}
	}
	max := 31
	if kind == "trends" || kind == "overview" {
		max = 366
	}
	if !q.From.Before(q.To) || q.To.Sub(q.From) > time.Duration(max)*24*time.Hour {
		fail(c, 400, "INVALID_RANGE", "时间区间为空或超过最大跨度")
		return q, false
	}
	return q, true
}
func within(t time.Time, q Query) bool { return !t.Before(q.From) && t.Before(q.To) }
func walk[T any](tx *bolt.Tx, name string, fn func(string, T) error) error {
	return bucket(tx, name).ForEach(func(k, v []byte) error {
		if v == nil {
			return nil
		}
		var x T
		if err := json.Unmarshal(v, &x); err != nil {
			return err
		}
		return fn(string(k), x)
	})
}
func (q Query) runMatches(r Run) bool {
	return (q.Provider == "" || r.Provider == q.Provider) && (q.Version == "" || r.AppVersion == q.Version) && (q.Instance == "" || r.InstallationID == q.Instance) && (q.Source == "" || r.SourceID == q.Source) && (q.Trigger == "" || r.Trigger == q.Trigger) && (q.Result == "" || r.OriginalResult == q.Result) && (q.Error == "" || r.Error != nil && *r.Error == q.Error) && !r.CreatedAt.After(q.AsOf)
}
func (q Query) auto(r Run) bool {
	if q.Trigger != "" {
		return r.Trigger == q.Trigger
	}
	return r.Trigger == "daily" || r.Trigger == "catch_up"
}
func runSlot(r Run) string {
	if r.ScheduledFor == nil {
		return ""
	}
	return day(*r.ScheduledFor) + "|" + r.InstallationID + "|" + r.AccountRef
}
func groupRun(r Run, key string) string {
	switch key {
	case "provider":
		return r.Provider
	case "source_id":
		return r.SourceID
	case "app_version":
		return r.AppVersion
	case "result":
		return r.OriginalResult
	case "error_code":
		if r.Error != nil {
			return *r.Error
		}
	}
	return "all"
}
func runningSets(tx *bolt.Tx) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	walk[RunningDay](tx, "running_days", func(_ string, d RunningDay) error {
		if out[d.InstallationID] == nil {
			out[d.InstallationID] = map[string]bool{}
		}
		out[d.InstallationID][d.Date] = true
		return nil
	})
	return out
}
func hasRecent(ds map[string]bool, at time.Time, n int) bool {
	for i := 0; i < n; i++ {
		if ds[day(at.AddDate(0, 0, -i))] {
			return true
		}
	}
	return false
}
func continuous(ds map[string]bool, at time.Time, n int) bool {
	for i := 1; i <= n; i++ {
		if !ds[day(at.AddDate(0, 0, -i))] {
			return false
		}
	}
	return true
}
func (q Query) instanceMatches(i Instance) bool {
	return (q.Instance == "" || q.Instance == i.ID) && (q.Version == "" || q.Version == i.AppVersion) && !i.RegisteredAt.After(q.AsOf)
}
func filteredInstances(tx *bolt.Tx, q Query) map[string]Instance {
	providers := map[string]bool{}
	sources := map[string]bool{}
	if q.Provider != "" {
		walk[Account](tx, "accounts", func(_ string, x Account) error {
			if x.Provider == q.Provider && !x.Removed {
				providers[x.InstallationID] = true
			}
			return nil
		})
	}
	if q.Source != "" {
		walk[Run](tx, "runs", func(_ string, r Run) error {
			if r.SourceID == q.Source {
				sources[r.InstallationID] = true
			}
			return nil
		})
	}
	out := map[string]Instance{}
	walk[Instance](tx, "instances", func(_ string, i Instance) error {
		if q.instanceMatches(i) && (q.Provider == "" || providers[i.ID]) && (q.Source == "" || sources[i.ID]) {
			out[i.ID] = i
		}
		return nil
	})
	return out
}
func ratio(n, d int) any {
	if d == 0 {
		return nil
	}
	return float64(n) / float64(d)
}
func (a *app) overview(c *gin.Context) {
	q, ok := a.query(c, "overview")
	if !ok {
		return
	}
	out := gin.H{"from": q.From, "to": q.To, "as_of": q.AsOf, "timezone": "Asia/Shanghai", "time_basis": "completed_at", "plan_time_basis": "scheduled_for", "triggers": []string{"daily", "catch_up"}, "expected_slots_estimated": true, "instance_filter_basis": "current snapshot; source_id uses known run association", "unsupported_metrics": []string{"search_count", "user_transfer_files", "active_users"}}
	if q.Trigger != "" {
		out["triggers"] = []string{q.Trigger}
	}
	count := map[string]int{}
	by := map[string]int{"baidu": 0, "quark": 0, "xunlei": 0}
	versions := map[string]int{}
	failures := map[string]int{}
	accountBy := map[string]int{"baidu": 0, "quark": 0, "xunlei": 0}
	validBy := map[string]int{"baidu": 0, "quark": 0, "xunlei": 0}
	executedBy := map[string]int{"baidu": 0, "quark": 0, "xunlei": 0}
	loggedIn := map[string]bool{}
	err := a.store.db.View(func(tx *bolt.Tx) error {
		insts := filteredInstances(tx, q)
		running := runningSets(tx)
		for id, i := range insts {
			count["registered_instances"]++
			versions[i.AppVersion]++
			if within(i.RegisteredAt, q) {
				count["new_instances"]++
			}
			if !i.Revoked && i.LastOnline != nil && !i.LastOnline.After(q.AsOf) && q.AsOf.Sub(*i.LastOnline) <= 30*time.Minute {
				count["online_instances"]++
			}
			if hasRecent(running[id], q.AsOf, 7) {
				count["running_instances_7d"]++
			}
			if hasRecent(running[id], q.AsOf, 30) {
				count["running_instances_30d"]++
			}
			if continuous(running[id], q.AsOf, 7) {
				count["continuous_running_instances_7d"]++
			}
			if continuous(running[id], q.AsOf, 30) {
				count["continuous_running_instances_30d"]++
			}
		}
		if err := walk[Account](tx, "accounts", func(_ string, acc Account) error {
			if _, ok := insts[acc.InstallationID]; !ok || acc.Removed || q.Provider != "" && acc.Provider != q.Provider {
				return nil
			}
			count["current_accounts"]++
			accountBy[acc.Provider]++
			if acc.AuthStatus == "expired" {
				count["expired_accounts"]++
			} else if acc.AuthStatus == "valid" && acc.AuthCheckedAt != nil && !acc.AuthCheckedAt.After(q.AsOf) && q.AsOf.Sub(*acc.AuthCheckedAt) <= 26*time.Hour {
				count["valid_accounts"]++
				validBy[acc.Provider]++
				loggedIn[acc.InstallationID] = true
			} else {
				count["unknown_accounts"]++
			}
			var r Run
			if acc.LastRunID != "" && get(tx, "runs", acc.InstallationID+"|"+acc.LastRunID, &r) && r.CycleCompletedAt != nil && !r.CycleCompletedAt.After(q.AsOf) && q.AsOf.Sub(*r.CycleCompletedAt) <= 26*time.Hour {
				count["functional_accounts"]++
			}
			return nil
		}); err != nil {
			return err
		}
		seenSlots, doneSlots, successSlots := map[string]bool{}, map[string]bool{}, map[string]bool{}
		if err := walk[Run](tx, "runs", func(_ string, r Run) error {
			if !q.runMatches(r) {
				return nil
			}
			if r.CycleCompleted && r.CycleCompletedAt != nil {
				if q.auto(r) {
					count["cumulative_successful_cycles"]++
					if within(*r.CycleCompletedAt, q) {
						count["successful_cycles"]++
						by[r.Provider]++
					}
				}
				if r.Trigger == "manual" && within(*r.CycleCompletedAt, q) {
					count["manual_successful_cycles"]++
				}
			}
			if q.auto(r) && within(runEvidenceTime(r), q) {
				count["executed_runs"]++
				executedBy[r.Provider]++
				if r.TransferConfirmed {
					count["transfer_confirmed_runs"]++
				}
				if r.CleanupPending {
					count["cleanup_pending_runs"]++
				}
				if r.TransferFailed {
					count["transfer_failed_runs"]++
				}
				if r.Unknown {
					count["unknown_runs"]++
				}
				if r.OriginalResult != "" {
					count["result_"+r.OriginalResult]++
				}
				if r.FailedStage != nil {
					code := ""
					if r.Error != nil {
						code = *r.Error
					}
					failures[r.Provider+"|"+*r.FailedStage+"|"+code+"|"+r.SourceVersion]++
				}
			}
			if r.Trigger != "manual" && r.ScheduledFor != nil && within(*r.ScheduledFor, q) {
				k := runSlot(r)
				seenSlots[k] = true
				if r.OriginalResult != "" {
					doneSlots[k] = true
				}
				if r.CycleCompleted {
					successSlots[k] = true
				}
			}
			return nil
		}); err != nil {
			return err
		}
		count["daily_successful_slots"] = len(successSlots)
		if q.Trigger != "manual" {
			if err := walk[Slot](tx, "slots", func(k string, s Slot) error {
				if !within(s.ScheduledFor, q) || q.Provider != "" && s.Provider != q.Provider {
					return nil
				}
				if _, ok := insts[s.InstallationID]; !ok {
					return nil
				}
				count["expected_daily_slots"]++
				if seenSlots[k] {
					count["started_daily_slots"]++
				} else {
					count["unreported_daily_slots"]++
					if q.AsOf.After(s.ScheduledFor.Add(10 * time.Minute)) {
						count["delayed_daily_slots"]++
					}
				}
				if doneSlots[k] {
					count["completed_daily_slots"]++
				}
				if successSlots[k] {
					count["passed_expected_daily_slots"]++
				}
				if i := insts[s.InstallationID]; i.ReceivedAt != nil && q.AsOf.Sub(*i.ReceivedAt) <= 7*24*time.Hour {
					count["expected_daily_slots_recent_instances"]++
				}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	for _, key := range []string{"successful_cycles", "manual_successful_cycles", "cumulative_successful_cycles", "daily_successful_slots", "transfer_confirmed_runs", "cleanup_pending_runs", "transfer_failed_runs", "unknown_runs", "expected_daily_slots", "unreported_daily_slots", "started_daily_slots", "completed_daily_slots", "passed_expected_daily_slots", "delayed_daily_slots", "expected_daily_slots_recent_instances", "registered_instances", "new_instances", "online_instances", "running_instances_7d", "running_instances_30d", "continuous_running_instances_7d", "continuous_running_instances_30d", "current_accounts", "valid_accounts", "expired_accounts", "unknown_accounts", "functional_accounts", "executed_runs", "result_passed", "result_failed", "result_skipped", "result_incomplete"} {
		out[key] = count[key]
	}
	out["successful_cycles_by_provider"] = by
	out["accounts_by_provider"] = accountBy
	out["valid_accounts_by_provider"] = validBy
	out["executed_runs_by_provider"] = executedBy
	out["logged_in_instances"] = len(loggedIn)
	out["versions_all_instances"] = versions
	out["stage_failure_distribution"] = failures
	out["coverage"] = gin.H{"numerator": count["completed_daily_slots"], "denominator": count["expected_daily_slots"], "value": ratio(count["completed_daily_slots"], count["expected_daily_slots"])}
	reply(c, 200, 0, "ok", out)
}
func trendBucket(t time.Time, g string) string {
	if g == "hour" {
		return t.In(beijing).Format("2006-01-02T15:00:00-07:00")
	}
	return day(t)
}
func (a *app) trends(c *gin.Context) {
	q, ok := a.query(c, "trends")
	if !ok {
		return
	}
	if !oneOf(q.Metric, "successful_cycles", "daily_successful_slots", "running_instances", "continuous_running_instances_7d", "continuous_running_instances_30d", "retention_d1", "retention_d7") {
		fail(c, 400, "METRIC_NOT_SUPPORTED", "此指标尚未接入；不以0代替未实现指标")
		return
	}
	cycles := oneOf(q.Metric, "successful_cycles", "daily_successful_slots")
	retention := strings.HasPrefix(q.Metric, "retention_")
	if !cycles && (q.Provider != "" || q.Source != "" || q.Trigger != "" || q.Granularity != "day" || q.Group != "" && q.Group != "app_version") {
		fail(c, 400, "UNSUPPORTED_COMBINATION", "运行与留存仅支持day、实例和版本筛选/分组")
		return
	}
	if q.Metric == "daily_successful_slots" && q.Group != "" && q.Group != "provider" {
		fail(c, 400, "UNSUPPORTED_COMBINATION", "每日计划槽仅支持provider分组，避免不同尝试版本/结果重复分摊同一槽")
		return
	}
	if q.Metric == "daily_successful_slots" && q.Trigger == "manual" {
		fail(c, 400, "UNSUPPORTED_COMBINATION", "手动检查不属于每日计划槽")
		return
	}
	counts := map[string]int{}
	dedup := map[string]bool{}
	items := []gin.H{}
	basis := "completed_at"
	if !cycles {
		basis = "evidence_date"
	}
	if q.Metric == "daily_successful_slots" {
		basis = "scheduled_for"
	}
	if retention {
		basis = "first_registered_date"
	}
	err := a.store.db.View(func(tx *bolt.Tx) error {
		if cycles {
			return walk[Run](tx, "runs", func(_ string, r Run) error {
				if !q.runMatches(r) || !q.auto(r) || !r.CycleCompleted || r.CycleCompletedAt == nil {
					return nil
				}
				t := *r.CycleCompletedAt
				if q.Metric == "daily_successful_slots" {
					if r.ScheduledFor == nil {
						return nil
					}
					t = *r.ScheduledFor
				}
				if !within(t, q) {
					return nil
				}
				key := trendBucket(t, q.Granularity) + "|" + groupRun(r, q.Group)
				if q.Metric == "daily_successful_slots" {
					d := key + "|" + runSlot(r)
					if dedup[d] {
						return nil
					}
					dedup[d] = true
				}
				counts[key]++
				return nil
			})
		}
		rs := runningSets(tx)
		insts := rows[Instance](tx, "instances")
		if retention {
			for d := midnight(q.From); d.Before(q.To); d = d.AddDate(0, 0, 1) {
				offset := 1
				if q.Metric == "retention_d7" {
					offset = 7
				}
				target := d.AddDate(0, 0, offset)
				matured := !q.AsOf.Before(target.AddDate(0, 0, 1))
				groups := map[string][2]int{"all": {0, 0}}
				if q.Group != "" {
					groups = map[string][2]int{}
				}
				for _, i := range insts {
					if day(i.RegisteredAt) != day(d) || !within(i.RegisteredAt, q) || q.Instance != "" && i.ID != q.Instance || q.Version != "" && i.FirstVersion != q.Version {
						continue
					}
					g := "all"
					if q.Group == "app_version" {
						g = i.FirstVersion
					}
					v := groups[g]
					v[1]++
					if rs[i.ID][day(target)] {
						v[0]++
					}
					groups[g] = v
				}
				for g, v := range groups {
					value := any(nil)
					numerator := any(nil)
					if matured {
						value = ratio(v[0], v[1])
						numerator = v[0]
					}
					items = append(items, gin.H{"bucket": day(d), "group": g, "value": value, "numerator": numerator, "denominator": v[1], "matured": matured})
				}
			}
			return nil
		}
		for d := midnight(q.From); d.Before(q.To); d = d.AddDate(0, 0, 1) {
			for _, i := range insts {
				if !q.instanceMatches(i) {
					continue
				}
				active := rs[i.ID][day(d)]
				if q.Metric == "continuous_running_instances_7d" {
					active = continuous(rs[i.ID], d, 7)
				}
				if q.Metric == "continuous_running_instances_30d" {
					active = continuous(rs[i.ID], d, 30)
				}
				if active {
					g := "all"
					if q.Group == "app_version" {
						g = i.AppVersion
					}
					counts[day(d)+"|"+g]++
				}
			}
		}
		return nil
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	if !retention {
		groups := map[string]bool{"all": true}
		if q.Group != "" {
			groups = map[string]bool{}
			if q.Group == "provider" {
				for _, p := range providers {
					if q.Provider == "" || q.Provider == p {
						groups[p] = true
					}
				}
			}
		}
		for k := range counts {
			parts := strings.SplitN(k, "|", 2)
			groups[parts[1]] = true
		}
		for d := midnight(q.From); d.Before(q.To); {
			if q.Granularity == "hour" && d.Before(q.From) {
				d = d.Add(time.Hour)
				continue
			}
			for g := range groups {
				key := trendBucket(d, q.Granularity) + "|" + g
				items = append(items, gin.H{"bucket": trendBucket(d, q.Granularity), "group": g, "value": counts[key]})
			}
			if q.Granularity == "hour" {
				d = d.Add(time.Hour)
			} else {
				d = d.AddDate(0, 0, 1)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["bucket"].(string)+"|"+items[i]["group"].(string) < items[j]["bucket"].(string)+"|"+items[j]["group"].(string)
	})
	reply(c, 200, 0, "ok", gin.H{"metric": q.Metric, "granularity": q.Granularity, "timezone": "Asia/Shanghai", "group_by": q.Group, "time_basis": basis, "version_basis": map[bool]string{true: "first_registered_version", false: "current snapshot for running; run version for cycles"}[retention], "from": q.From, "to": q.To, "items": items, "as_of": q.AsOf})
}

var errPageFull = errors.New("page full")

func (a *app) listAnalytics(c *gin.Context, kind string) {
	if kind == "slots" {
		a.listSlots(c)
		return
	}
	q, ok := a.query(c, kind)
	if !ok {
		return
	}
	items := []any{}
	keys := []string{}
	more := false
	err := a.store.db.View(func(tx *bolt.Tx) error {
		insts := filteredInstances(tx, q)
		b := kind
		if kind == "checks" {
			b = "runs"
		}
		cur := bucket(tx, b).Cursor()
		k, v := cur.First()
		if q.After != "" {
			k, v = cur.Seek([]byte(q.After))
			if string(k) == q.After {
				k, v = cur.Next()
			}
		}
		for ; k != nil; k, v = cur.Next() {
			var item any
			match := false
			switch kind {
			case "instances":
				var i Instance
				if json.Unmarshal(v, &i) != nil {
					continue
				}
				_, match = insts[i.ID]
				if match {
					item = gin.H{"instance": safeInstance(i), "online": !i.Revoked && i.LastOnline != nil && !i.LastOnline.After(q.AsOf) && q.AsOf.Sub(*i.LastOnline) <= 30*time.Minute}
				}
			case "accounts":
				var x Account
				if json.Unmarshal(v, &x) != nil {
					continue
				}
				_, match = insts[x.InstallationID]
				match = match && (q.Provider == "" || q.Provider == x.Provider)
				item = x
			case "checks":
				var x Run
				if json.Unmarshal(v, &x) != nil {
					continue
				}
				match = q.runMatches(x) && within(runEvidenceTime(x), q)
				item = x.safe()
			case "events":
				var e StoredEvent
				if json.Unmarshal(v, &e) != nil {
					continue
				}
				var d EventData
				json.Unmarshal(e.Data, &d)
				var r Run
				get(tx, "runs", e.InstallationID+"|"+d.RunID, &r)
				match = within(e.OccurredAt, q) && !e.ReceivedAt.After(q.AsOf) && q.runMatches(r) && (q.Version == "" || e.AppVersion == q.Version)
				e.Hash = ""
				item = e
			case "audit":
				var e Audit
				if json.Unmarshal(v, &e) != nil {
					continue
				}
				match = within(e.ReceivedAt, q) && !e.ReceivedAt.After(q.AsOf) && (c.Query("actor") == "" || c.Query("actor") == e.Actor) && (c.Query("endpoint_id") == "" || c.Query("endpoint_id") == e.Endpoint) && (c.Query("status") == "" || c.Query("status") == strconv.Itoa(e.Status))
				item = e
			}
			if match {
				if len(items) == q.Limit {
					more = true
					break
				}
				items = append(items, item)
				keys = append(keys, string(k))
			}
		}
		return nil
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	var next any
	if more {
		raw, _ := json.Marshal(pageCursor{keys[len(keys)-1], q.AsOf, q.Fingerprint})
		next = a.signCursor(raw)
	}
	reply(c, 200, 0, "ok", gin.H{"items": items, "next_cursor": next, "as_of": q.AsOf, "time_basis": map[bool]string{true: "current_snapshot", false: "occurred_at"}[kind == "instances" || kind == "accounts"], "consistency": "keyset; as_of bounds receipt of new records, existing rows may reflect later updates"})
}
func (a *app) checkDetail(c *gin.Context) {
	if len(c.Request.URL.Query()) != 1 || len(c.Request.URL.Query()["installation_id"]) != 1 || !validID(c.Query("installation_id")) || !validID(c.Param("run_id")) {
		fail(c, 400, "INVALID_QUERY", "需要唯一的 installation_id 与 run_id")
		return
	}
	var r Run
	events := []StoredEvent{}
	attempts := []Run{}
	found := false
	err := a.store.db.View(func(tx *bolt.Tx) error {
		found = get(tx, "runs", c.Query("installation_id")+"|"+c.Param("run_id"), &r)
		if !found {
			return nil
		}
		if err := walk[StoredEvent](tx, "events", func(_ string, e StoredEvent) error {
			var d EventData
			json.Unmarshal(e.Data, &d)
			if e.InstallationID == r.InstallationID && d.RunID == r.ID {
				e.Hash = ""
				events = append(events, e)
			}
			return nil
		}); err != nil {
			return err
		}
		if r.ScheduledFor != nil {
			return walk[Run](tx, "runs", func(_ string, x Run) error {
				if runSlot(x) == runSlot(r) {
					attempts = append(attempts, x.safe())
				}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		a.dbError(c, err)
		return
	}
	if !found {
		fail(c, 404, "NOT_FOUND", "检查不存在")
		return
	}
	sort.Slice(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	sort.Slice(attempts, func(i, j int) bool { return runEvidenceTime(attempts[i]).Before(runEvidenceTime(attempts[j])) })
	reply(c, 200, 0, "ok", gin.H{"run": r.safe(), "events": events, "slot_attempts": attempts})
}
