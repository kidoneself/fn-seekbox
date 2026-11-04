package cloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pansou/util/json"
)

const bxEntryLimit = 10000
const bxPageLimit = 100
const bxDepthLimit = 32

// bxNumber accepts the integer/string representations used by both web APIs
// without ever converting a file identifier through float64.
type bxNumber string

type baiduInt64 int64

func (n *baiduInt64) UnmarshalJSON(b []byte) error {
	var raw bxNumber
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw == "" {
		return errors.New("百度整数响应为空")
	}
	value, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return err
	}
	*n = baiduInt64(value)
	return nil
}

func (n *bxNumber) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*n = bxNumber(s)
		return nil
	}
	var v json.Number
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*n = bxNumber(v.String())
	return nil
}

func bxClient() *http.Client {
	return &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || len(via) == 0 || req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
			return errors.New("网盘响应重定向到其他地址，已停止")
		}
		return nil
	}}
}

func bxRead(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		// url.Error may contain a credential-bearing query. Never expose it.
		return nil, errors.New("网盘请求失败或超时，请稍后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrAuthExpired
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("网盘拒绝请求，请在官方网盘检查账号验证或权限")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("网盘服务返回 HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return nil, errors.New("读取网盘响应失败")
	}
	if len(b) > 8<<20 {
		return nil, errors.New("网盘响应超出大小限制")
	}
	return b, nil
}

func bxName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 1024 && !strings.ContainsAny(name, "/\\\x00\r\n")
}

func bxRevision(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func bxWait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type baiduProvider struct {
	client    *http.Client
	baseURL   string
	pollDelay time.Duration
}

func NewBaiduProvider() Provider {
	return &baiduProvider{client: bxClient(), baseURL: "https://pan.baidu.com", pollDelay: 500 * time.Millisecond}
}
func (p *baiduProvider) ID() string     { return "baidu" }
func (p *baiduProvider) RootID() string { return "/" }

type baiduSession struct {
	provider            *baiduProvider
	client              *http.Client
	token               string
	shareID, shareOwner string
}

func (p *baiduProvider) session(c Credentials) (*baiduSession, error) {
	raw := strings.TrimSpace(c["cookie"])
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return nil, fmt.Errorf("请填写本人百度网盘完整 Cookie: %w", ErrAuthExpired)
	}
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(p.baseURL)
	r := &http.Request{Header: http.Header{"Cookie": []string{raw}}}
	for _, cookie := range r.Cookies() {
		cookie.Path = "/"
		jar.SetCookies(u, []*http.Cookie{cookie})
	}
	client := *p.client
	client.Jar = jar
	return &baiduSession{provider: p, client: &client}, nil
}

type baiduRunKey struct{}
type baiduRunSession struct{ session *baiduSession }

func (p *baiduProvider) authSession(ctx context.Context, c Credentials) (*baiduSession, error) {
	run, _ := ctx.Value(baiduRunKey{}).(*baiduRunSession)
	if run != nil && run.session != nil && run.session.provider == p {
		return run.session, nil
	}
	session, err := p.session(c)
	if err != nil {
		return nil, err
	}
	if _, err = session.login(ctx); err != nil {
		return nil, err
	}
	if run != nil {
		run.session = session
	}
	return session, nil
}

func (s *baiduSession) request(ctx context.Context, method, endpoint string, q, form url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	q.Set("web", "1")
	q.Set("clienttype", "0")
	q.Set("channel", "chunlei")
	// Public Baidu web protocol identifier, not an OAuth credential.
	q.Set("app_id", "250528")
	if s.token != "" {
		q.Set("bdstoken", s.token)
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, s.provider.baseURL+endpoint+"?"+q.Encode(), body)
	if err != nil {
		return errors.New("无法创建百度网盘请求")
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://pan.baidu.com/disk/main")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	b, err := bxRead(s.client, req)
	if err != nil {
		return err
	}
	if raw, ok := out.(*string); ok {
		*raw = string(b)
		return nil
	}
	var status struct {
		Errno *int `json:"errno"`
	}
	if json.Unmarshal(b, &status) != nil || status.Errno == nil {
		return errors.New("百度网盘返回格式异常，请检查网页登录状态")
	}
	if *status.Errno != 0 {
		switch *status.Errno {
		case -6, 111, 112:
			return ErrAuthExpired
		case -9:
			return errors.New("百度分享提取码错误")
		case -12, 8001:
			return errors.New("百度要求人工验证，请先在官网完成验证")
		case -30, -8, 4:
			return ErrDestinationExists
		default:
			return fmt.Errorf("百度网盘操作失败（错误码 %d）", *status.Errno)
		}
	}
	if err := json.Unmarshal(b, out); err != nil {
		return errors.New("百度网盘响应字段不完整")
	}
	return nil
}

func (s *baiduSession) login(ctx context.Context) (Identity, error) {
	var out struct {
		Result struct {
			Token    string   `json:"bdstoken"`
			UK       bxNumber `json:"uk"`
			Username string   `json:"username"`
		} `json:"result"`
	}
	err := s.request(ctx, "GET", "/api/gettemplatevariable", url.Values{"fields": {`["bdstoken","uk","username"]`}}, nil, &out)
	if err != nil {
		return Identity{}, err
	}
	if out.Result.Token == "" || out.Result.UK == "" || out.Result.UK == "0" {
		return Identity{}, ErrAuthExpired
	}
	s.token = out.Result.Token
	name := out.Result.Username
	if name == "" {
		name = "百度网盘 " + string(out.Result.UK)
	}
	return Identity{ID: string(out.Result.UK), Name: name}, nil
}

func (p *baiduProvider) Validate(ctx context.Context, c Credentials) (Identity, error) {
	s, err := p.session(c)
	if err != nil {
		return Identity{}, err
	}
	return s.login(ctx)
}

type baiduFile struct {
	ID    bxNumber   `json:"fs_id"`
	Name  string     `json:"server_filename"`
	Path  string     `json:"path"`
	IsDir baiduInt64 `json:"isdir"`
	Size  baiduInt64 `json:"size"`
	MD5   string     `json:"md5"`
	MTime baiduInt64 `json:"server_mtime"`
}

func (s *baiduSession) files(ctx context.Context, endpoint string, query url.Values) ([]baiduFile, error) {
	all := make([]baiduFile, 0)
	seen := map[string]bool{}
	for page := 1; page <= bxPageLimit; page++ {
		query.Set("page", strconv.Itoa(page))
		query.Set("num", "100")
		var out struct {
			List    []baiduFile `json:"list"`
			HasMore *int        `json:"has_more"`
			ShareID bxNumber    `json:"share_id"`
			UK      bxNumber    `json:"uk"`
		}
		if err := s.request(ctx, "GET", endpoint, query, nil, &out); err != nil {
			return nil, err
		}
		if out.List == nil {
			return nil, errors.New("百度网盘未返回文件列表")
		}
		if endpoint == "/share/list" && query.Get("root") == "1" {
			if out.ShareID != "" {
				s.shareID = string(out.ShareID)
			}
			if out.UK != "" {
				s.shareOwner = string(out.UK)
			}
		}
		for _, f := range out.List {
			if f.ID == "" || !bxName(f.Name) || seen[string(f.ID)] {
				return nil, errors.New("百度网盘目录包含重复或异常文件")
			}
			seen[string(f.ID)] = true
			all = append(all, f)
		}
		if len(all) > bxEntryLimit {
			return nil, errors.New("百度网盘目录超过 10000 项，请选择较小的目录")
		}
		if len(out.List) == 0 || (out.HasMore != nil && *out.HasMore == 0) || (out.HasMore == nil && len(out.List) < 100) {
			return all, nil
		}
	}
	return nil, errors.New("百度网盘目录超过分页限制")
}

func baiduPath(id string) (string, error) {
	if id == "" {
		return "/", nil
	}
	if !strings.HasPrefix(id, "/") || strings.ContainsAny(id, "\x00\r\n") {
		return "", errors.New("百度目录路径无效")
	}
	return path.Clean(id), nil
}

func (p *baiduProvider) ListFolders(ctx context.Context, c Credentials, parent string) ([]Folder, error) {
	dir, err := baiduPath(parent)
	if err != nil {
		return nil, err
	}
	s, err := p.authSession(ctx, c)
	if err != nil {
		return nil, err
	}
	files, err := s.files(ctx, "/api/list", url.Values{"dir": {dir}, "order": {"name"}, "desc": {"0"}})
	if err != nil {
		return nil, err
	}
	result := make([]Folder, 0)
	for _, f := range files {
		if f.IsDir == 1 {
			result = append(result, Folder{ID: path.Join(dir, f.Name), Name: f.Name})
		}
	}
	return result, nil
}

func (p *baiduProvider) CreateFolder(ctx context.Context, c Credentials, parent, name string) (Folder, error) {
	if !bxName(name) {
		return Folder{}, errors.New("目录名称无效")
	}
	dir, err := baiduPath(parent)
	if err != nil {
		return Folder{}, err
	}
	s, err := p.authSession(ctx, c)
	if err != nil {
		return Folder{}, err
	}
	files, err := s.files(ctx, "/api/list", url.Values{"dir": {dir}})
	if err != nil {
		return Folder{}, err
	}
	for _, f := range files {
		if f.Name == name {
			if f.IsDir == 1 {
				return Folder{ID: path.Join(dir, name), Name: name}, nil
			}
			return Folder{}, errors.New("目标已存在同名文件，无法创建目录")
		}
	}
	var out struct {
		ID    bxNumber   `json:"fs_id"`
		Path  string     `json:"path"`
		IsDir baiduInt64 `json:"isdir"`
	}
	target := path.Join(dir, name)
	err = s.request(ctx, "POST", "/api/create", url.Values{"a": {"commit"}}, url.Values{"path": {target}, "isdir": {"1"}, "block_list": {"[]"}, "rtype": {"0"}}, &out)
	if err != nil {
		return Folder{}, err
	}
	if out.ID == "" || out.Path != target || out.IsDir != 1 {
		return Folder{}, errors.New("百度网盘未确认目录创建成功")
	}
	return Folder{ID: target, Name: name}, nil
}

var baiduShareID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (p *baiduProvider) ReadShare(ctx context.Context, c Credentials, link ShareLink) (Share, error) {
	u, err := url.Parse(strings.TrimSpace(link.URL))
	if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host != "pan.baidu.com" {
		return Share{}, errors.New("仅支持 pan.baidu.com 的百度分享链接")
	}
	short := ""
	if strings.HasPrefix(u.Path, "/s/1") {
		short = strings.TrimPrefix(u.Path, "/s/1")
	} else if u.Path == "/share/init" {
		short = u.Query().Get("surl")
	}
	if !baiduShareID.MatchString(short) {
		return Share{}, errors.New("百度分享链接格式无效")
	}
	password := strings.TrimSpace(link.Password)
	if password == "" {
		password = u.Query().Get("pwd")
	}
	s, err := p.authSession(ctx, c)
	if err != nil {
		return Share{}, err
	}
	sekey := ""
	if password != "" {
		var verify struct {
			RandSK string `json:"randsk"`
		}
		err = s.request(ctx, "POST", "/share/verify", url.Values{"surl": {short}}, url.Values{"pwd": {password}, "vcode": {""}, "vcode_str": {""}}, &verify)
		if err != nil {
			return Share{}, err
		}
		if verify.RandSK == "" {
			return Share{}, errors.New("百度网盘未确认提取码验证结果")
		}
		sekey = verify.RandSK
		if decoded, err := url.QueryUnescape(sekey); err == nil {
			sekey = decoded
		}
		base, _ := url.Parse(p.baseURL)
		s.client.Jar.SetCookies(base, []*http.Cookie{{Name: "BDCLND", Value: url.QueryEscape(sekey), Path: "/"}})
	}
	rootFiles, err := s.files(ctx, "/share/list", url.Values{"shorturl": {short}, "root": {"1"}, "sekey": {sekey}})
	if err != nil {
		return Share{}, err
	}
	shareID, owner := s.shareID, s.shareOwner
	var html string
	if shareID == "" || owner == "" {
		if err = s.request(ctx, "GET", "/s/1"+short, nil, nil, &html); err != nil {
			return Share{}, err
		}
	}
	getID := func(key string) string {
		matches := regexp.MustCompile(`"` + key + `"\s*:\s*"?([0-9]+)"?`).FindStringSubmatch(html)
		if len(matches) == 2 {
			return matches[1]
		}
		return ""
	}
	if shareID == "" {
		shareID = getID("shareid")
	}
	if owner == "" {
		owner = getID("share_uk")
	}
	if owner == "" {
		owner = getID("uk")
	}
	if shareID == "" || owner == "" {
		return Share{}, errors.New("无法读取百度分享信息，请检查提取码、分享有效性及 Cookie")
	}
	share := Share{ID: shareID, Entries: []Entry{}, Data: map[string]string{"shorturl": short, "owner": owner, "bdstoken": s.token, "sekey": sekey}}
	base, _ := url.Parse(p.baseURL)
	for _, cookie := range s.client.Jar.Cookies(base) {
		if cookie.Name == "BDCLND" || cookie.Name == "PANPSC" {
			share.Data[cookie.Name] = cookie.Value
		}
	}
	visited := map[string]bool{}
	var walk func(string, string, string, int) error
	walk = func(remote, rel, parent string, depth int) error {
		if depth > bxDepthLimit {
			return errors.New("百度分享目录层级超过限制")
		}
		query := url.Values{"shorturl": {short}, "shareid": {shareID}, "uk": {owner}, "root": {"1"}}
		if remote != "" {
			query.Set("root", "0")
			query.Set("dir", remote)
		}
		files := rootFiles
		if remote != "" {
			var err error
			files, err = s.files(ctx, "/share/list", query)
			if err != nil {
				return err
			}
		}
		for _, f := range files {
			if visited[string(f.ID)] {
				return errors.New("百度分享存在循环或重复文件标识")
			}
			visited[string(f.ID)] = true
			e := Entry{ID: string(f.ID), Name: f.Name, Path: path.Join(rel, f.Name), ParentID: parent, IsDir: f.IsDir == 1, Size: int64(f.Size), Token: f.MD5, Revision: bxRevision(string(f.ID), f.MD5, strconv.FormatInt(int64(f.Size), 10), strconv.FormatInt(int64(f.MTime), 10))}
			share.Entries = append(share.Entries, e)
			if len(share.Entries) > bxEntryLimit {
				return errors.New("百度分享超过 10000 项，请选择较小的分享")
			}
			if e.IsDir {
				if f.Path == "" || !strings.HasPrefix(f.Path, "/") {
					return errors.New("百度分享目录路径缺失")
				}
				if err := walk(f.Path, e.Path, e.ID, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk("", "", "", 0); err != nil {
		return Share{}, err
	}
	return share, nil
}

func (p *baiduProvider) Save(ctx context.Context, c Credentials, share Share, entry Entry, target string) error {
	_, err := p.SaveBatch(ctx, c, share, []Entry{entry}, target)
	return err
}

func (p *baiduProvider) SaveBatch(ctx context.Context, c Credentials, share Share, entries []Entry, target string) (BatchResult, error) {
	result := BatchResult{}
	ids, names, err := batchEntries(entries)
	if err != nil {
		return result, err
	}
	for _, id := range ids {
		number, err := strconv.ParseUint(id, 10, 64)
		if err != nil || number == 0 {
			return result, errors.New("百度文件标识无效")
		}
	}
	dir, err := baiduPath(target)
	if err != nil {
		return result, err
	}
	session, err := p.authSession(ctx, c)
	if err != nil {
		return result, err
	}
	base, _ := url.Parse(p.baseURL)
	for _, name := range []string{"BDCLND", "PANPSC"} {
		if value := share.Data[name]; value != "" {
			session.client.Jar.SetCookies(base, []*http.Cookie{{Name: name, Value: value, Path: "/"}})
		}
	}
	files, err := session.files(ctx, "/api/list", url.Values{"dir": {dir}})
	if err != nil {
		return result, err
	}
	previousIDs := map[string]bool{}
	for _, f := range files {
		if names[f.Name] {
			return result, ErrDestinationExists
		}
		previousIDs[string(f.ID)] = true
	}
	var out struct {
		Info []struct {
			Errno int `json:"errno"`
		} `json:"info"`
		Extra struct {
			List []struct {
				From bxNumber `json:"from_fs_id"`
				To   bxNumber `json:"to_fs_id"`
			} `json:"list"`
		} `json:"extra"`
	}
	result.Submitted = true
	err = session.request(ctx, "POST", "/share/transfer", url.Values{"shareid": {share.ID}, "from": {share.Data["owner"]}, "sekey": {share.Data["sekey"]}, "ondup": {"newcopy"}, "async": {"1"}}, url.Values{"fsidlist": {"[" + strings.Join(ids, ",") + "]"}, "path": {dir}}, &out)
	if err != nil {
		return result, err
	}
	for _, item := range out.Info {
		if item.Errno != 0 {
			return result, fmt.Errorf("百度批次包含转存失败项（错误码 %d），请到网盘核对", item.Errno)
		}
	}
	confirmed := map[string]bool{}
	requested := map[string]bool{}
	for _, id := range ids {
		requested[id] = true
	}
	targets := map[string]bool{}
	for _, item := range out.Extra.List {
		source, target := string(item.From), string(item.To)
		if requested[source] && target != "" && target != "0" && !confirmed[source] && !targets[target] && !previousIDs[target] {
			confirmed[source] = true
			targets[target] = true
			result.Confirmed = append(result.Confirmed, source)
		}
	}
	if len(confirmed) == len(entries) {
		return result, nil
	}
	// Match each remaining source file against a NEW destination file; a count
	// increase or a subset of confirmed files is not whole-batch success.
	confirmCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for attempt := 0; attempt < 60; attempt++ {
		current, err := session.files(confirmCtx, "/api/list", url.Values{"dir": {dir}})
		if err != nil {
			return result, err
		}
		for _, e := range entries {
			if confirmed[e.ID] {
				continue
			}
			for _, f := range current {
				if f.ID != "" && f.ID != "0" && !targets[string(f.ID)] && !previousIDs[string(f.ID)] && f.Name == e.Name && f.IsDir == 0 && int64(f.Size) == e.Size && (e.Token == "" || f.MD5 == e.Token) {
					confirmed[e.ID] = true
					targets[string(f.ID)] = true
					result.Confirmed = append(result.Confirmed, e.ID)
					break
				}
			}
		}
		if len(confirmed) == len(entries) {
			return result, nil
		}
		if err := bxWait(confirmCtx, p.pollDelay); err != nil {
			return result, err
		}
	}
	return result, errors.New("百度已接收请求但未确认本批全部文件，请在网盘确认后重试")
}
