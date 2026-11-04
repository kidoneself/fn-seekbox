package cloud

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pansou/util/json"
)

// Xunlei uses a browser access token and a separate captcha token. These are
// deliberately supplied by the account owner; this adapter contains no borrowed
// client secret, signing key, refresh credential or captcha-solving service.
type xunleiProvider struct {
	client           *http.Client
	baseURL, userURL string
	pollDelay        time.Duration
}

func NewXunleiProvider() Provider {
	return &xunleiProvider{client: bxClient(), baseURL: "https://api-pan.xunlei.com/drive/v1", userURL: "https://xluser-ssl.xunlei.com/v1", pollDelay: time.Second}
}
func (p *xunleiProvider) ID() string     { return "xunlei" }
func (p *xunleiProvider) RootID() string { return "" }

type xunleiWebSession struct {
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token"`
	ExpiresAt    time.Time         `json:"expires_at"`
	Headers      map[string]string `json:"headers"`
	CaptchaMeta  map[string]string `json:"captcha_meta,omitempty"`
}

var xunleiSessionHeaders = []string{"x-device-id", "x-client-id", "x-captcha-token", "x-client-version", "x-device-sign", "x-protocol-version", "x-sdk-version"}

func xunleiSession(c Credentials) (*xunleiWebSession, error) {
	if strings.TrimSpace(c["web_session"]) == "" {
		return nil, nil
	}
	var s xunleiWebSession
	if err := json.Unmarshal([]byte(c["web_session"]), &s); err != nil || strings.TrimSpace(s.AccessToken) == "" || strings.TrimSpace(s.RefreshToken) == "" {
		return nil, errors.New("迅雷网页会话 JSON 不完整，需要本人 access_token、refresh_token 和 headers")
	}
	normalized := make(map[string]string)
	for key, value := range s.Headers {
		normalized[strings.ToLower(key)] = strings.TrimSpace(value)
	}
	s.Headers = make(map[string]string)
	for _, key := range xunleiSessionHeaders {
		value := normalized[key]
		if len(value) > 16384 || strings.ContainsAny(value, "\r\n\x00") {
			return nil, errors.New("迅雷会话请求头格式无效")
		}
		if value != "" {
			s.Headers[key] = value
		}
	}
	for _, key := range []string{"x-device-id", "x-client-id", "x-captcha-token"} {
		if s.Headers[key] == "" {
			return nil, errors.New("迅雷网页会话缺少设备、客户端或验证令牌")
		}
	}
	if len(s.CaptchaMeta) > 32 {
		return nil, errors.New("迅雷验证元数据过长")
	}
	for key, value := range s.CaptchaMeta {
		if len(key) > 128 || len(value) > 16384 || strings.ContainsAny(key+value, "\r\n\x00") {
			return nil, errors.New("迅雷验证元数据格式无效")
		}
	}
	if strings.ContainsAny(s.AccessToken+s.RefreshToken, "\r\n\x00") {
		return nil, errors.New("迅雷会话令牌格式无效")
	}
	return &s, nil
}

func (s *xunleiWebSession) credentials() Credentials {
	c := Credentials{"access_token": s.AccessToken, "captcha_token": s.Headers["x-captcha-token"], "device_id": s.Headers["x-device-id"], "client_id": s.Headers["x-client-id"]}
	for _, key := range xunleiSessionHeaders {
		if value := s.Headers[key]; value != "" {
			c[key] = value
		}
	}
	return c
}

type xunleiFailure struct {
	kind   string
	status int
	code   int64
}

func (e *xunleiFailure) Error() string {
	if e.Is(ErrAuthExpired) {
		return "迅雷登录或验证令牌已失效，请重新连接"
	}
	return fmt.Sprintf("迅雷请求失败（HTTP %d，错误码 %d）", e.status, e.code)
}
func (e *xunleiFailure) Is(target error) bool {
	if target != ErrAuthExpired {
		return false
	}
	return e.status == 401 || e.kind == "unauthenticated" || e.kind == "invalid_token" || e.kind == "invalid_grant" || e.kind == "token_expired" || e.kind == "captcha_invalid" || e.kind == "captcha_required" || e.kind == "captcha_expired"
}
func (e *xunleiFailure) captcha() bool {
	return e.kind == "captcha_invalid" || e.kind == "captcha_required" || e.kind == "captcha_expired"
}

func persistXunleiSession(ctx context.Context, c Credentials, s *xunleiWebSession) error {
	encoded, err := json.Marshal(s)
	if err != nil {
		return errors.New("无法保存迅雷续期会话")
	}
	next := make(Credentials, len(c))
	for key, value := range c {
		next[key] = value
	}
	next["web_session"] = string(encoded)
	if err := CommitCredentials(ctx, next); err != nil {
		return errors.New("迅雷凭据已续期但安全保存失败，已停止后续请求，请重新连接")
	}
	for key, value := range next {
		c[key] = value
	}
	return nil
}

func (p *xunleiProvider) refreshAccess(ctx context.Context, c Credentials, session *xunleiWebSession) error {
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	err := p.requestOnce(ctx, session.credentials(), "POST", "/auth/token", nil, map[string]string{"client_id": session.Headers["x-client-id"], "grant_type": "refresh_token", "refresh_token": session.RefreshToken}, &out)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out.AccessToken) == "" || strings.ContainsAny(out.AccessToken+out.RefreshToken, "\r\n\x00") || out.ExpiresIn <= 0 || out.ExpiresIn > 365*24*3600 {
		return errors.New("迅雷访问令牌刷新响应不完整，请重新连接")
	}
	next := *session
	next.AccessToken = out.AccessToken
	if out.RefreshToken != "" {
		next.RefreshToken = out.RefreshToken
	}
	margin := int64(120)
	if out.ExpiresIn < 240 {
		margin = out.ExpiresIn / 10
	}
	next.ExpiresAt = time.Now().Add(time.Duration(out.ExpiresIn-margin) * time.Second)
	if err := persistXunleiSession(ctx, c, &next); err != nil {
		return err
	}
	*session = next
	return nil
}

func (p *xunleiProvider) refreshCaptcha(ctx context.Context, c Credentials, session *xunleiWebSession, method, endpoint string) error {
	if session.CaptchaMeta["captcha_sign"] == "" {
		return fmt.Errorf("迅雷验证令牌已过期，当前会话不含续期元数据，请重新连接: %w", ErrAuthExpired)
	}
	apiPath := "/drive/v1" + endpoint
	if endpoint == "/user/me" {
		apiPath = "/v1" + endpoint
	}
	var out struct {
		Token string `json:"captcha_token"`
		URL   string `json:"url"`
	}
	err := p.requestOnce(ctx, session.credentials(), "POST", "/shield/captcha/init", nil, map[string]any{"action": method + ":" + apiPath, "captcha_token": session.Headers["x-captcha-token"], "client_id": session.Headers["x-client-id"], "device_id": session.Headers["x-device-id"], "meta": session.CaptchaMeta}, &out)
	if err != nil {
		return err
	}
	if out.URL != "" {
		return fmt.Errorf("迅雷需要人工安全验证，请重新连接: %w", ErrAuthExpired)
	}
	if out.Token == "" || strings.ContainsAny(out.Token, "\r\n\x00") {
		return errors.New("迅雷验证令牌刷新响应不完整")
	}
	next := *session
	next.Headers = make(map[string]string, len(session.Headers))
	for key, value := range session.Headers {
		next.Headers[key] = value
	}
	next.Headers["x-captcha-token"] = out.Token
	if err := persistXunleiSession(ctx, c, &next); err != nil {
		return err
	}
	*session = next
	return nil
}

func (p *xunleiProvider) request(ctx context.Context, c Credentials, method, endpoint string, query url.Values, data, out any) error {
	session, err := xunleiSession(c)
	if err != nil {
		return err
	}
	if session == nil {
		return p.requestOnce(ctx, c, method, endpoint, query, data, out)
	}
	accessRefreshed, captchaRefreshed := false, false
	if session.ExpiresAt.IsZero() || !time.Now().Before(session.ExpiresAt) {
		if err := p.refreshAccess(ctx, c, session); err != nil {
			return err
		}
		accessRefreshed = true
	}
	for {
		err := p.requestOnce(ctx, session.credentials(), method, endpoint, query, data, out)
		var rejected *xunleiFailure
		if !errors.As(err, &rejected) {
			return err
		}
		if rejected.captcha() && !captchaRefreshed {
			if err := p.refreshCaptcha(ctx, c, session, method, endpoint); err != nil {
				return err
			}
			captchaRefreshed = true
			continue
		}
		if !rejected.captcha() && errors.Is(rejected, ErrAuthExpired) && !accessRefreshed {
			if err := p.refreshAccess(ctx, c, session); err != nil {
				return err
			}
			accessRefreshed = true
			continue
		}
		return err
	}
}

func (p *xunleiProvider) requestOnce(ctx context.Context, c Credentials, method, endpoint string, query url.Values, data, out any) error {
	for _, name := range []string{"access_token", "captcha_token", "device_id", "client_id"} {
		if strings.TrimSpace(c[name]) == "" || strings.ContainsAny(c[name], "\r\n\x00") {
			return fmt.Errorf("请更新本人迅雷网页的 %s: %w", name, ErrAuthExpired)
		}
	}
	var body io.Reader
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return errors.New("迅雷请求参数无效")
		}
		body = bytes.NewReader(b)
	}
	base := p.baseURL
	if endpoint == "/user/me" || endpoint == "/auth/token" || endpoint == "/shield/captcha/init" {
		base = p.userURL
	}
	address := base + endpoint
	if len(query) != 0 {
		address += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return errors.New("无法创建迅雷请求")
	}
	token := strings.TrimSpace(c["access_token"])
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	if endpoint != "/auth/token" && endpoint != "/shield/captcha/init" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-Captcha-Token", strings.TrimSpace(c["captcha_token"]))
	req.Header.Set("X-Device-Id", strings.TrimSpace(c["device_id"]))
	req.Header.Set("X-Client-Id", strings.TrimSpace(c["client_id"]))
	for _, key := range xunleiSessionHeaders {
		if value := c[key]; value != "" {
			req.Header.Set(key, value)
		}
	}
	req.Header.Set("Origin", "https://pan.xunlei.com")
	req.Header.Set("Referer", "https://pan.xunlei.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("迅雷请求失败或超时，请先检查网盘中的实际结果")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(b) > 8<<20 {
		return errors.New("迅雷响应读取失败或超出大小限制")
	}
	var status struct {
		ErrorCode   int64  `json:"error_code"`
		Error       string `json:"error"`
		ShareStatus string `json:"share_status"`
	}
	if json.Unmarshal(b, &status) != nil {
		if resp.StatusCode == 401 {
			return &xunleiFailure{status: 401}
		}
		return errors.New("迅雷返回格式异常，请检查网页登录状态")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || status.ErrorCode != 0 || (status.Error != "" && status.Error != "success") {
		switch status.Error {
		case "review_panel":
			return errors.New("迅雷要求人工验证，请先在官网完成验证")
		default:
			return &xunleiFailure{kind: status.Error, status: resp.StatusCode, code: status.ErrorCode}
		}
	}
	if status.ShareStatus != "" && status.ShareStatus != "OK" {
		switch status.ShareStatus {
		case "PASS_CODE_EMPTY":
			return errors.New("该迅雷分享需要提取码")
		case "PASS_CODE_ERROR":
			return errors.New("迅雷分享提取码错误")
		case "EXPIRED", "DELETED", "NOT_FOUND":
			return errors.New("迅雷分享已失效或被删除")
		case "USER_VIEW_RISK":
			return errors.New("迅雷要求验证当前分享，请在官网完成验证")
		default:
			return errors.New("迅雷分享当前不可访问或不可转存")
		}
	}
	if err := json.Unmarshal(b, out); err != nil {
		return errors.New("迅雷响应字段不完整")
	}
	return nil
}

func (p *xunleiProvider) Validate(ctx context.Context, c Credentials) (Identity, error) {
	var out struct {
		Sub    string `json:"sub"`
		UserID string `json:"user_id"`
		Name   string `json:"name"`
	}
	if err := p.request(ctx, c, "GET", "/user/me", nil, nil, &out); err != nil {
		return Identity{}, err
	}
	if out.Sub == "" && out.UserID == "" {
		return Identity{}, ErrAuthExpired
	}
	// Check that the captcha/device context also grants access to the drive.
	if _, err := p.files(ctx, c, ""); err != nil {
		return Identity{}, err
	}
	if out.Name == "" {
		out.Name = "迅雷云盘用户"
	}
	id := out.Sub
	if id == "" {
		id = out.UserID
	}
	return Identity{ID: id, Name: out.Name}, nil
}

type xunleiFile struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	ParentID     string   `json:"parent_id"`
	Kind         string   `json:"kind"`
	Size         bxNumber `json:"size"`
	Revision     bxNumber `json:"revision"`
	Hash         string   `json:"hash"`
	ModifiedTime string   `json:"modified_time"`
	Phase        string   `json:"phase"`
}

func (p *xunleiProvider) files(ctx context.Context, c Credentials, parent string) ([]xunleiFile, error) {
	query := url.Values{"parent_id": {parent}, "limit": {"100"}, "space": {""}, "filters": {`{"trashed":{"eq":false}}`}}
	return p.pages(ctx, c, "/files", query, nil)
}

func (p *xunleiProvider) pages(ctx context.Context, c Credentials, endpoint string, query url.Values, shareToken *string) ([]xunleiFile, error) {
	all := make([]xunleiFile, 0)
	seenPages, seenIDs := map[string]bool{}, map[string]bool{}
	for page := 0; page < bxPageLimit; page++ {
		var out struct {
			Files         []xunleiFile `json:"files"`
			FileInfos     []xunleiFile `json:"file_infos"`
			Next          string       `json:"next_page_token"`
			PassCodeToken string       `json:"pass_code_token"`
		}
		if shareToken != nil {
			query.Set("pass_code_token", *shareToken)
		}
		if err := p.request(ctx, c, "GET", endpoint, query, nil, &out); err != nil {
			return nil, err
		}
		if out.Files == nil {
			out.Files = out.FileInfos
		}
		if out.Files == nil {
			return nil, errors.New("迅雷未返回文件列表")
		}
		if shareToken != nil && out.PassCodeToken != "" {
			*shareToken = out.PassCodeToken
		}
		for _, file := range out.Files {
			if !xunleiID.MatchString(file.ID) || !bxName(file.Name) || seenIDs[file.ID] {
				return nil, errors.New("迅雷目录包含重复或异常文件")
			}
			seenIDs[file.ID] = true
			all = append(all, file)
		}
		if len(all) > bxEntryLimit {
			return nil, errors.New("迅雷目录超过 10000 项，请选择较小的目录")
		}
		if out.Next == "" {
			return all, nil
		}
		if seenPages[out.Next] {
			return nil, errors.New("迅雷目录分页重复，已停止")
		}
		seenPages[out.Next] = true
		query.Set("page_token", out.Next)
	}
	return nil, errors.New("迅雷目录超过分页限制")
}

var xunleiID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

func (p *xunleiProvider) ListFolders(ctx context.Context, c Credentials, parent string) ([]Folder, error) {
	if parent != "" && !xunleiID.MatchString(parent) {
		return nil, errors.New("迅雷目录标识无效")
	}
	files, err := p.files(ctx, c, parent)
	if err != nil {
		return nil, err
	}
	result := make([]Folder, 0)
	for _, file := range files {
		if file.Kind == "drive#folder" {
			result = append(result, Folder{ID: file.ID, Name: file.Name})
		}
	}
	return result, nil
}

func (p *xunleiProvider) CreateFolder(ctx context.Context, c Credentials, parent, name string) (Folder, error) {
	if !bxName(name) || (parent != "" && !xunleiID.MatchString(parent)) {
		return Folder{}, errors.New("迅雷目录名称或标识无效")
	}
	files, err := p.files(ctx, c, parent)
	if err != nil {
		return Folder{}, err
	}
	for _, f := range files {
		if f.Name == name {
			if f.Kind == "drive#folder" {
				return Folder{ID: f.ID, Name: f.Name}, nil
			}
			return Folder{}, errors.New("目标已存在同名文件，无法创建目录")
		}
	}
	var response struct {
		xunleiFile
		File *xunleiFile `json:"file"`
	}
	err = p.request(ctx, c, "POST", "/files", nil, map[string]any{"kind": "drive#folder", "name": name, "parent_id": parent, "space": ""}, &response)
	if err != nil {
		return Folder{}, err
	}
	// The create endpoint wraps its result in "file"; accept older direct
	// responses too, without treating an incomplete result as success.
	out := response.xunleiFile
	if response.File != nil {
		out = *response.File
	}
	if !xunleiID.MatchString(out.ID) || out.Kind != "drive#folder" || out.Name != name {
		return Folder{}, errors.New("迅雷未确认目录创建成功")
	}
	return Folder{ID: out.ID, Name: out.Name}, nil
}

func (p *xunleiProvider) ReadShare(ctx context.Context, c Credentials, link ShareLink) (Share, error) {
	u, err := url.Parse(strings.TrimSpace(link.URL))
	if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host != "pan.xunlei.com" || !strings.HasPrefix(u.Path, "/s/") {
		return Share{}, errors.New("仅支持 pan.xunlei.com 的迅雷分享链接")
	}
	id := strings.TrimPrefix(u.Path, "/s/")
	if !xunleiID.MatchString(id) {
		return Share{}, errors.New("迅雷分享链接格式无效")
	}
	password := strings.TrimSpace(link.Password)
	if password == "" {
		password = u.Query().Get("pwd")
	}
	if password == "" {
		password = u.Query().Get("password")
	}
	share := Share{ID: id, Entries: []Entry{}, Data: map[string]string{}}
	shareToken := ""
	seen := map[string]bool{}
	var walk func(string, string, []string, int) error
	walk = func(parent, rel string, ancestors []string, depth int) error {
		if depth > bxDepthLimit {
			return errors.New("迅雷分享目录层级超过限制")
		}
		endpoint := "/share"
		query := url.Values{"share_id": {id}, "pass_code": {password}, "limit": {"100"}, "thumbnail_size": {"SIZE_SMALL"}}
		if parent != "" {
			endpoint = "/share/detail"
			query.Set("parent_id", parent)
		}
		files, err := p.pages(ctx, c, endpoint, query, &shareToken)
		if err != nil {
			return err
		}
		for _, file := range files {
			if seen[file.ID] {
				return errors.New("迅雷分享包含循环或重复文件标识")
			}
			seen[file.ID] = true
			if file.Phase != "" && file.Phase != "PHASE_TYPE_COMPLETE" {
				return errors.New("迅雷分享仍有未完成或异常文件，请稍后重试")
			}
			isDir := file.Kind == "drive#folder"
			if !isDir && file.Kind != "drive#file" {
				return errors.New("迅雷分享包含不支持的文件类型")
			}
			size := int64(0)
			if file.Size != "" {
				var err error
				size, err = strconv.ParseInt(string(file.Size), 10, 64)
				if err != nil || size < 0 {
					return errors.New("迅雷文件大小无效")
				}
			}
			ancestorJSON, _ := json.Marshal(ancestors)
			e := Entry{ID: file.ID, Name: file.Name, Path: path.Join(rel, file.Name), ParentID: parent, IsDir: isDir, Size: size, Token: string(ancestorJSON), Revision: bxRevision(file.ID, string(file.Revision), file.Hash, string(file.Size), file.ModifiedTime)}
			share.Entries = append(share.Entries, e)
			if len(share.Entries) > bxEntryLimit {
				return errors.New("迅雷分享超过 10000 项，请选择较小的分享")
			}
			if isDir {
				next := append(append([]string{}, ancestors...), file.ID)
				if err := walk(file.ID, e.Path, next, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk("", "", []string{}, 0); err != nil {
		return Share{}, err
	}
	share.Data["pass_code_token"] = shareToken
	return share, nil
}

func (p *xunleiProvider) Save(ctx context.Context, c Credentials, share Share, entry Entry, target string) error {
	_, err := p.SaveBatch(ctx, c, share, []Entry{entry}, target)
	return err
}

func (p *xunleiProvider) SaveBatch(ctx context.Context, c Credentials, share Share, entries []Entry, target string) (BatchResult, error) {
	result := BatchResult{}
	ids, names, err := batchEntries(entries)
	if err != nil {
		return result, err
	}
	entry := entries[0]
	if entry.IsDir || !xunleiID.MatchString(entry.ID) || !xunleiID.MatchString(share.ID) || !bxName(entry.Name) || (target != "" && !xunleiID.MatchString(target)) {
		return result, errors.New("迅雷转存参数无效")
	}
	for _, item := range entries {
		if !xunleiID.MatchString(item.ID) || item.Token != entry.Token {
			return result, errors.New("迅雷批次文件或祖先目录上下文不一致")
		}
	}
	files, err := p.files(ctx, c, target)
	if err != nil {
		return result, err
	}
	for _, f := range files {
		if names[f.Name] {
			return result, ErrDestinationExists
		}
	}
	ancestors := []string{}
	if entry.Token != "" {
		if err := json.Unmarshal([]byte(entry.Token), &ancestors); err != nil {
			return result, errors.New("迅雷分享目录上下文无效")
		}
	}
	for _, id := range ancestors {
		if !xunleiID.MatchString(id) {
			return result, errors.New("迅雷分享目录上下文无效")
		}
	}
	var out struct {
		TaskID        string `json:"task_id"`
		RestoreTaskID string `json:"restore_task_id"`
		FileID        string `json:"file_id"`
	}
	result.Submitted = true
	err = p.request(ctx, c, "POST", "/share/restore", nil, map[string]any{"parent_id": target, "share_id": share.ID, "pass_code_token": share.Data["pass_code_token"], "ancestor_ids": ancestors, "file_ids": ids, "specify_parent_id": true}, &out)
	if err != nil {
		return result, err
	}
	if out.RestoreTaskID != "" {
		out.TaskID = out.RestoreTaskID
	}
	if out.TaskID == "" {
		return result, errors.New("迅雷已接收请求但未返回可确认的转存任务，请到网盘确认")
	}
	if !xunleiID.MatchString(out.TaskID) {
		return result, errors.New("迅雷返回的转存任务标识无效")
	}
	taskCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for attempt := 0; attempt < 60; attempt++ {
		var task struct {
			Phase  string `json:"phase"`
			Params struct {
				Partial   json.RawMessage `json:"partial_failed"`
				ErrorCode bxNumber        `json:"error_code"`
			} `json:"params"`
		}
		err := p.request(taskCtx, c, "GET", "/tasks/"+out.TaskID, nil, nil, &task)
		if err != nil {
			return result, err
		}
		partial := string(task.Params.Partial)
		if task.Phase == "PHASE_TYPE_ERROR" || task.Phase == "ERROR" || partial == "true" || partial == `"true"` || (task.Params.ErrorCode != "" && task.Params.ErrorCode != "0") {
			return result, errors.New("迅雷转存任务失败或仅部分成功，请在官网查看任务详情")
		}
		if task.Phase == "PHASE_TYPE_COMPLETE" || task.Phase == "COMPLETE" {
			result.Confirmed = ids
			return result, nil
		}
		if err := bxWait(taskCtx, p.pollDelay); err != nil {
			return result, err
		}
	}
	return result, errors.New("迅雷转存尚未确认完成，请到网盘查看任务后再重试")
}
