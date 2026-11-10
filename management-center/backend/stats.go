package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	bolt "go.etcd.io/bbolt"
)

var statsBucket = []byte("public-stats-v1")
var beijing = time.FixedZone("Asia/Shanghai", 8*60*60)

type Counts struct {
	Total  uint64 `json:"total"`
	OK     uint64 `json:"ok"`
	Errors uint64 `json:"errors"`
}
type DayCounts struct {
	Date string `json:"date"`
	Counts
}
type EndpointCounts struct {
	Endpoint string `json:"endpoint"`
	Counts
}
type ProviderCounts struct {
	Provider string `json:"provider"`
	Counts
}
type StatusCounts struct {
	Status int    `json:"status"`
	Total  uint64 `json:"total"`
}
type Stats struct {
	Timezone   string           `json:"timezone"`
	Days       int              `json:"days"`
	Today      DayCounts        `json:"today"`
	Period     Counts           `json:"period"`
	Daily      []DayCounts      `json:"daily"`
	ByEndpoint []EndpointCounts `json:"by_endpoint"`
	ByProvider []ProviderCounts `json:"by_provider"`
	ByStatus   []StatusCounts   `json:"by_status"`
}

func (s *Store) recordStat(now time.Time, endpoint, provider string, status int) error {
	date := now.In(beijing).Format("2006-01-02")
	oldest := now.In(beijing).AddDate(0, 0, -89).Format("2006-01-02")
	key := []byte(fmt.Sprintf("%s|%s|%s|%03d", date, endpoint, provider, status))
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(statsBucket)
		if err != nil {
			return err
		}
		count := uint64(0)
		if raw := bucket.Get(key); len(raw) == 8 {
			count = binary.BigEndian.Uint64(raw)
		}
		raw := make([]byte, 8)
		binary.BigEndian.PutUint64(raw, count+1)
		if err := bucket.Put(key, raw); err != nil {
			return err
		}
		cursor := bucket.Cursor()
		for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
			if len(k) < 10 || string(k[:10]) < oldest {
				if err := cursor.Delete(); err != nil {
					return err
				}
			} else {
				break
			}
		}
		return nil
	})
}
func addCounts(c *Counts, n uint64, status int) {
	c.Total += n
	if status >= 200 && status < 300 {
		c.OK += n
	} else {
		c.Errors += n
	}
}
func (s *Store) stats(now time.Time, days int) (Stats, error) {
	now = now.In(beijing)
	today := now.Format("2006-01-02")
	start := now.AddDate(0, 0, -days+1).Format("2006-01-02")
	result := Stats{Timezone: "Asia/Shanghai", Days: days, Today: DayCounts{Date: today}, Daily: make([]DayCounts, 0, days), ByEndpoint: []EndpointCounts{}, ByProvider: []ProviderCounts{}, ByStatus: []StatusCounts{}}
	daily := make(map[string]*Counts)
	endpoints := map[string]*Counts{"/api/check-link": {}, "/api/search-sites": {}}
	providerCounts := map[string]*Counts{"baidu": {}, "quark": {}, "xunlei": {}, "invalid": {}, "none": {}}
	statuses := make(map[int]uint64)
	for i := 0; i < days; i++ {
		date := now.AddDate(0, 0, -days+1+i).Format("2006-01-02")
		daily[date] = &Counts{}
	}
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(statsBucket)
		if bucket == nil {
			return nil
		}
		cursor := bucket.Cursor()
		for key, raw := cursor.Seek([]byte(start)); key != nil; key, raw = cursor.Next() {
			parts := strings.Split(string(key), "|")
			if len(parts) != 4 || len(raw) != 8 {
				return errors.New("invalid statistics record")
			}
			if parts[0] > today {
				break
			}
			day, ok := daily[parts[0]]
			if !ok {
				continue
			}
			status, err := strconv.Atoi(parts[3])
			if err != nil {
				return errors.New("invalid statistics status")
			}
			n := binary.BigEndian.Uint64(raw)
			endpoint, ok := endpoints[parts[1]]
			if !ok {
				return errors.New("invalid statistics endpoint")
			}
			provider, ok := providerCounts[parts[2]]
			if !ok {
				return errors.New("invalid statistics provider")
			}
			addCounts(day, n, status)
			addCounts(endpoint, n, status)
			addCounts(provider, n, status)
			addCounts(&result.Period, n, status)
			statuses[status] += n
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	result.Today.Counts = *daily[today]
	for i := 0; i < days; i++ {
		date := now.AddDate(0, 0, -days+1+i).Format("2006-01-02")
		result.Daily = append(result.Daily, DayCounts{Date: date, Counts: *daily[date]})
	}
	for _, endpoint := range []string{"/api/check-link", "/api/search-sites"} {
		result.ByEndpoint = append(result.ByEndpoint, EndpointCounts{Endpoint: endpoint, Counts: *endpoints[endpoint]})
	}
	for _, provider := range []string{"baidu", "quark", "xunlei", "invalid", "none"} {
		result.ByProvider = append(result.ByProvider, ProviderCounts{Provider: provider, Counts: *providerCounts[provider]})
	}
	for status, n := range statuses {
		result.ByStatus = append(result.ByStatus, StatusCounts{Status: status, Total: n})
	}
	sort.Slice(result.ByStatus, func(i, j int) bool { return result.ByStatus[i].Status < result.ByStatus[j].Status })
	return result, nil
}
func (a *app) countPublic(c *gin.Context) {
	now := a.now()
	c.Next()
	if c.Request.Method != "GET" {
		return
	}
	endpoint := c.FullPath()
	if endpoint != "/api/check-link" && endpoint != "/api/search-sites" {
		return
	}
	provider := "none"
	if endpoint == "/api/check-link" {
		provider = "invalid"
		query, err := url.ParseQuery(c.Request.URL.RawQuery)
		if values := query["provider"]; err == nil && len(values) == 1 && validProvider(values[0]) {
			provider = values[0]
		}
	}
	if err := a.store.recordStat(now, endpoint, provider, c.Writer.Status()); err != nil {
		log.Printf("public statistics update failed: %v", err)
	}
}
func (a *app) getStats(c *gin.Context) {
	days := 7
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		fail(c, 400, 40001, "days 必须是 1 到 90 的整数")
		return
	}
	if values, ok := query["days"]; ok {
		if len(values) != 1 || values[0] == "" {
			fail(c, 400, 40001, "days 必须且只能出现一次，为 1 到 90 的整数")
			return
		}
		days, err = strconv.Atoi(values[0])
		if err != nil || days < 1 || days > 90 {
			fail(c, 400, 40001, "days 必须是 1 到 90 的整数")
			return
		}
	}
	result, err := a.store.stats(a.now(), days)
	if err != nil {
		a.dbError(c, err)
		return
	}
	reply(c, 200, 0, "ok", result)
}
