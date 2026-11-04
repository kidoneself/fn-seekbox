package aipan

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"pansou/model"
	"pansou/plugin"
	pjson "pansou/util/json"
)

const (
	pluginName           = "aipan"
	pluginPriority       = 2
	baseURL              = "https://www.aipan.me"
	requestTimeout       = 12 * time.Second
	maxResponseBytes     = 8 << 20
	maxSearchItems       = 12
	maxLinksPerMovie     = 64
	maxDetailConcurrency = 6
)

var passwordRE = regexp.MustCompile(`(?i)(?:提取码|访问码|密码|取件码|password|pwd|code)\s*[:：]?\s*([0-9a-zA-Z_-]{3,32})`)

// AipanPlugin searches 爱盼's movie catalog and returns its pan/magnet resources.
type AipanPlugin struct {
	*plugin.BaseAsyncPlugin
}

type searchResponse struct {
	Movies []searchMovie `json:"movies"`
}

type searchMovie struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Rate     string   `json:"rate"`
	Cover    string   `json:"cover"`
	Year     int      `json:"year"`
	Genres   []string `json:"genres"`
	Subtitle string   `json:"subtitle"`
}

type detailResponse struct {
	AipanID      int        `json:"aipanId"`
	Title        string     `json:"title"`
	Aka          string     `json:"aka"`
	Year         int        `json:"year"`
	Synopsis     string     `json:"synopsis"`
	Genres       []string   `json:"genres"`
	Region       string     `json:"region"`
	Directors    []string   `json:"directors"`
	Actors       []string   `json:"actors"`
	ReleaseLabel string     `json:"releaseLabel"`
	Language     string     `json:"language"`
	IMDBRating   float64    `json:"imdbRating"`
	PanLinks     []panLink  `json:"panLinks"`
	Resources    []resource `json:"resources"`
	PosterURL    string     `json:"posterUrl"`
}

type panLink struct {
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	SizeLabel string `json:"sizeLabel"`
	Own       bool   `json:"own"`
}

type resource struct {
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Name      string `json:"name"`
	SizeLabel string `json:"sizeLabel"`
	Category  string `json:"category"`
}

func init() {
	plugin.RegisterGlobalPlugin(NewAipanPlugin())
}

func NewAipanPlugin() *AipanPlugin {
	return &AipanPlugin{BaseAsyncPlugin: plugin.NewBaseAsyncPlugin(pluginName, pluginPriority)}
}

func (p *AipanPlugin) Name() string        { return pluginName }
func (p *AipanPlugin) DisplayName() string { return "爱盼" }
func (p *AipanPlugin) Description() string {
	return "爱盼 - 夸克、光鸭、百度、迅雷和磁力影视资源"
}

func (p *AipanPlugin) Search(keyword string, ext map[string]interface{}) ([]model.SearchResult, error) {
	result, err := p.SearchWithResult(keyword, ext)
	if err != nil {
		return nil, err
	}
	return result.Results, nil
}

func (p *AipanPlugin) SearchWithResult(keyword string, ext map[string]interface{}) (model.PluginSearchResult, error) {
	return p.AsyncSearchWithResult(keyword, p.searchImpl, p.MainCacheKey, ext)
}

func (p *AipanPlugin) searchImpl(client *http.Client, keyword string, _ map[string]interface{}) ([]model.SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []model.SearchResult{}, nil
	}
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}

	items, err := p.searchMovies(client, keyword)
	if err != nil {
		return nil, fmt.Errorf("[%s] 搜索请求失败: %w", p.Name(), err)
	}
	if len(items) == 0 {
		return []model.SearchResult{}, nil
	}
	if len(items) > maxSearchItems {
		items = items[:maxSearchItems]
	}

	results := make([]model.SearchResult, len(items))
	valid := make([]bool, len(items))
	sem := make(chan struct{}, maxDetailConcurrency)
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		go func(index int, item searchMovie) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			result, ok := p.fetchDetail(client, item)
			if ok {
				results[index] = result
				valid[index] = true
			}
		}(i, item)
	}
	wg.Wait()

	out := make([]model.SearchResult, 0, len(items))
	for i := range results {
		if valid[i] && len(results[i].Links) > 0 {
			out = append(out, results[i])
		}
	}
	return plugin.FilterResultsByKeyword(out, keyword), nil
}

func (p *AipanPlugin) searchMovies(client *http.Client, keyword string) ([]searchMovie, error) {
	target := baseURL + "/api/movies/search?q=" + url.QueryEscape(keyword)
	body, err := p.fetchBody(client, target, baseURL+"/")
	if err != nil {
		return nil, err
	}
	var response searchResponse
	if err := pjson.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("解析搜索响应失败: %w", err)
	}
	items := make([]searchMovie, 0, len(response.Movies))
	seen := make(map[string]struct{})
	for _, item := range response.Movies {
		item.ID = strings.TrimSpace(item.ID)
		item.Title = cleanText(item.Title)
		if item.ID == "" || item.Title == "" {
			continue
		}
		if _, exists := seen[item.ID]; exists {
			continue
		}
		seen[item.ID] = struct{}{}
		items = append(items, item)
	}
	return items, nil
}

func (p *AipanPlugin) fetchDetail(client *http.Client, item searchMovie) (model.SearchResult, bool) {
	target := baseURL + "/api/movies/detail/" + url.PathEscape(item.ID)
	body, err := p.fetchBody(client, target, baseURL+"/movie/"+url.PathEscape(item.ID))
	if err != nil {
		return model.SearchResult{}, false
	}
	var detail detailResponse
	if err := pjson.Unmarshal(body, &detail); err != nil {
		return model.SearchResult{}, false
	}
	if detail.Title == "" {
		detail.Title = item.Title
	}
	links := convertLinks(detail)
	if len(links) == 0 {
		return model.SearchResult{}, false
	}
	for i := range links {
		links[i].WorkTitle = detail.Title
	}
	content := detailContent(detail)
	tags := []string{"爱盼"}
	tags = append(tags, detail.Genres...)
	if detail.Region != "" {
		tags = append(tags, detail.Region)
	}
	if detail.Year > 0 {
		tags = append(tags, fmt.Sprintf("%d", detail.Year))
	}
	if detail.Language != "" {
		tags = append(tags, detail.Language)
	}
	cover := strings.TrimSpace(detail.PosterURL)
	if cover == "" {
		cover = strings.TrimSpace(item.Cover)
	}
	uniqueID := item.ID
	if uniqueID == "" && detail.AipanID > 0 {
		uniqueID = fmt.Sprintf("%d", detail.AipanID)
	}
	result := model.SearchResult{
		UniqueID:  fmt.Sprintf("%s-%s", p.Name(), uniqueID),
		MessageID: fmt.Sprintf("%s-%s", p.Name(), uniqueID),
		Channel:   "",
		Datetime:  time.Now(),
		Title:     detail.Title,
		Content:   content,
		Tags:      dedupeStrings(tags),
		Links:     links,
	}
	if cover != "" {
		result.Images = []string{cover}
	}
	return result, true
}

func convertLinks(detail detailResponse) []model.Link {
	links := make([]model.Link, 0, len(detail.PanLinks)+len(detail.Resources))
	seen := make(map[string]struct{})
	add := func(kind, rawURL, label string) {
		rawURL = cleanLink(rawURL)
		if !validLinkURL(rawURL) {
			return
		}
		linkType := linkType(kind, rawURL)
		if linkType == "" {
			return
		}
		password := extractPassword(rawURL)
		if password == "" {
			password = extractPassword(label)
		}
		key := linkType + "\x00" + rawURL + "\x00" + password
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		links = append(links, model.Link{Type: linkType, URL: rawURL, Password: password})
	}
	for _, item := range detail.PanLinks {
		add(item.Kind, item.URL, item.Title)
		if len(links) >= maxLinksPerMovie {
			return links
		}
	}
	for _, item := range detail.Resources {
		add(item.Kind, item.URL, item.Name)
		if len(links) >= maxLinksPerMovie {
			return links
		}
	}
	return links
}

func linkType(kind, rawURL string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "magnet" && strings.HasPrefix(strings.ToLower(rawURL), "magnet:") {
		return "magnet"
	}
	if kind == "ed2k" && strings.HasPrefix(strings.ToLower(rawURL), "ed2k:") {
		return "ed2k"
	}
	if kind == "alipan" || kind == "aliyun" || kind == "aliyundrive" || kind == "ali" {
		return "aliyun"
	}
	if kind == "guangya" || kind == "guangya_pan" {
		return "guangya"
	}
	if kind == "quark" {
		return "quark"
	}
	if kind == "baidu" {
		return "baidu"
	}
	if kind == "uc" {
		return "uc"
	}
	if kind == "xunlei" {
		return "xunlei"
	}
	if kind == "tianyi" || kind == "189cloud" {
		return "tianyi"
	}
	if kind == "115" {
		return "115"
	}
	if kind == "123" || kind == "123pan" {
		return "123"
	}
	if kind == "pikpak" {
		return "pikpak"
	}
	if kind == "mobile" || kind == "139" {
		return "mobile"
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case strings.HasPrefix(strings.ToLower(rawURL), "magnet:"):
		return "magnet"
	case strings.HasPrefix(strings.ToLower(rawURL), "ed2k:"):
		return "ed2k"
	case host == "pan.quark.cn":
		return "quark"
	case host == "pan.baidu.com" || host == "yun.baidu.com":
		return "baidu"
	case host == "drive.uc.cn":
		return "uc"
	case host == "pan.xunlei.com":
		return "xunlei"
	case host == "cloud.189.cn":
		return "tianyi"
	case host == "www.guangyapan.com" || host == "guangyapan.com":
		return "guangya"
	case host == "alipan.com" || strings.HasSuffix(host, ".alipan.com") || host == "aliyundrive.com" || strings.HasSuffix(host, ".aliyundrive.com"):
		return "aliyun"
	case host == "115.com" || strings.HasSuffix(host, ".115.com"):
		return "115"
	case host == "123pan.com" || strings.HasSuffix(host, ".123pan.com"):
		return "123"
	case host == "mypikpak.com" || strings.HasSuffix(host, ".mypikpak.com"):
		return "pikpak"
	case host == "caiyun.139.com" || host == "caiyun.feixin.10086.cn":
		return "mobile"
	default:
		return ""
	}
}

func validLinkURL(rawURL string) bool {
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	if strings.HasPrefix(lower, "magnet:") || strings.HasPrefix(lower, "ed2k:") {
		return true
	}
	u, err := url.Parse(rawURL)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}

func detailContent(detail detailResponse) string {
	parts := make([]string, 0, 8)
	if detail.Aka != "" {
		parts = append(parts, "别名："+cleanText(detail.Aka))
	}
	if detail.Year > 0 {
		parts = append(parts, fmt.Sprintf("年份：%d", detail.Year))
	}
	if detail.ReleaseLabel != "" {
		parts = append(parts, "上映："+cleanText(detail.ReleaseLabel))
	}
	if detail.Region != "" {
		parts = append(parts, "地区："+cleanText(detail.Region))
	}
	if len(detail.Genres) > 0 {
		parts = append(parts, "类型："+strings.Join(dedupeStrings(detail.Genres), ", "))
	}
	if len(detail.Directors) > 0 {
		parts = append(parts, "导演："+strings.Join(dedupeStrings(detail.Directors), " / "))
	}
	if len(detail.Actors) > 0 {
		parts = append(parts, "主演："+strings.Join(dedupeStrings(detail.Actors), " / "))
	}
	if detail.Language != "" {
		parts = append(parts, "语言："+cleanText(detail.Language))
	}
	if detail.IMDBRating > 0 {
		parts = append(parts, fmt.Sprintf("评分：%.1f", detail.IMDBRating))
	}
	if detail.Synopsis != "" {
		parts = append(parts, cleanText(detail.Synopsis))
	}
	resourceNames := make([]string, 0, len(detail.Resources))
	for _, item := range detail.Resources {
		name := cleanText(item.Name)
		if name != "" {
			if item.SizeLabel != "" {
				name += " [" + cleanText(item.SizeLabel) + "]"
			}
			resourceNames = append(resourceNames, name)
		}
	}
	if len(resourceNames) > 0 {
		parts = append(parts, "资源："+strings.Join(resourceNames, "；"))
	}
	return strings.Join(parts, " | ")
}

func (p *AipanPlugin) fetchBody(client *http.Client, target, referer string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Referer", referer)
	req.Header.Set("Origin", baseURL)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
}

func cleanLink(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "\"'<>),.;")
	return strings.ReplaceAll(raw, "&amp;", "&")
}

func extractPassword(raw string) string {
	u, err := url.Parse(raw)
	if err == nil {
		for _, key := range []string{"pwd", "password", "passcode", "code"} {
			if value := strings.TrimSpace(u.Query().Get(key)); value != "" {
				return value
			}
		}
	}
	if match := passwordRE.FindStringSubmatch(raw); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func cleanText(value string) string {
	value = strings.ReplaceAll(value, "\u00a0", " ")
	return strings.Join(strings.Fields(value), " ")
}

func dedupeStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		value = cleanText(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
