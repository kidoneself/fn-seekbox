package cloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"pansou/util/json"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	quarkDriveURL         = "https://drive-pc.quark.cn/1/clouddrive"
	quarkAccountURL       = "https://pan.quark.cn/account/info"
	quarkPageSize         = 100
	quarkMaxPages         = 200
	quarkMaxEntries       = 10000
	quarkMaxDepth         = 32
	quarkMaxResponseBytes = 8 << 20
)

// quarkProvider implements the Quark browser API using a user's own Cookie.
// It does not use the separate official OAuth SDK or any shared credentials.
type quarkProvider struct {
	client       *http.Client
	pollInterval time.Duration
	taskTimeout  time.Duration
}

func NewQuarkProvider() Provider {
	return &quarkProvider{
		client: &http.Client{
			Timeout: 30 * time.Second,
			// A redirect must never forward a user's Cookie to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		pollInterval: time.Second,
		taskTimeout:  15 * time.Minute,
	}
}

func (*quarkProvider) ID() string     { return "quark" }
func (*quarkProvider) RootID() string { return "0" }

type quarkEnvelope struct {
	Code     json.RawMessage `json:"code"`
	Success  *bool           `json:"success"`
	Status   int             `json:"status"`
	Data     json.RawMessage `json:"data"`
	Metadata struct {
		Total *int `json:"_total"`
	} `json:"metadata"`
}

type quarkFile struct {
	ID        string          `json:"fid"`
	Name      string          `json:"file_name"`
	Dir       *bool           `json:"dir"`
	FileType  *int            `json:"file_type"`
	Size      int64           `json:"size"`
	Token     string          `json:"share_fid_token"`
	UpdatedAt json.RawMessage `json:"updated_at"`
	MD5       string          `json:"md5"`
	SHA1      string          `json:"sha1"`
}

func (f quarkFile) isDir() bool {
	if f.Dir != nil {
		return *f.Dir
	}
	return f.FileType != nil && *f.FileType == 0
}

func (f quarkFile) revision() string {
	// Sharing tokens expire and must not be part of the stable fingerprint.
	value := f.ID + "\x00" + f.Name + "\x00" + strconv.FormatInt(f.Size, 10) + "\x00" + string(f.UpdatedAt) + "\x00" + f.MD5 + "\x00" + f.SHA1
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func quarkCode(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return "unknown"
}

var errQuarkListingInconsistent = errors.New("夸克文件列表暂未同步，请稍后重试")

var errQuarkOwnShare = errors.New("夸克不允许转存当前账号自己的分享，请换用另一账号提供的小文件")

func quarkAPIError(code string, status int) error {
	if code == "31001" || code == "31004" || code == "NO_AUTH" || strings.HasPrefix(code, "AUTH_ERROR") || status == http.StatusUnauthorized {
		return ErrAuthExpired
	}
	// Never include upstream messages: some gateways echo request credentials.
	switch code {
	case "41017":
		return errQuarkOwnShare
	case "32003":
		return errors.New("夸克未完成转存（错误码 32003），请检查网盘实际结果后重试")
	case "32004":
		return errors.New("文件超过夸克账号允许的单文件大小")
	case "41035", "41014":
		return errors.New("已达到夸克账号转存数量限制")
	case "41007":
		return errors.New("夸克分享不存在或已失效")
	case "41008":
		return errors.New("夸克分享访问令牌已失效，请重新检查分享")
	case "41013":
		return errors.New("夸克目标文件夹不存在")
	case "23008":
		return errors.New("夸克目标位置已存在同名文件或目录")
	}
	if len(code) > 32 {
		code = "unknown"
	}
	for _, c := range code {
		if !(c >= '0' && c <= '9') && !(c >= 'A' && c <= 'Z') && c != '_' && c != '-' {
			code = "unknown"
			break
		}
	}
	return fmt.Errorf("夸克请求失败（代码 %s，状态 %d）", code, status)
}

func (p *quarkProvider) request(ctx context.Context, credentials Credentials, method, endpoint string, query url.Values, body any) (quarkEnvelope, error) {
	var result quarkEnvelope
	cookie := strings.TrimSpace(credentials["cookie"])
	if cookie == "" {
		return result, ErrAuthExpired
	}
	if strings.ContainsAny(cookie, "\r\n") {
		return result, errors.New("夸克 Cookie 格式不正确")
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("pr", "ucpro")
	query.Set("fr", "pc")
	query.Set("uc_param_str", "")
	u := quarkDriveURL + endpoint
	if endpoint == "/account/info" {
		u = quarkAccountURL
		query.Set("platform", "pc")
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return result, errors.New("无法构建夸克请求")
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, u+"?"+query.Encode(), payload)
	if err != nil {
		return result, errors.New("无法构建夸克请求")
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://pan.quark.cn")
	req.Header.Set("Referer", "https://pan.quark.cn/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		// net/url errors contain the query string, including the share token.
		return result, errors.New("连接夸克网盘失败，请稍后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return result, ErrAuthExpired
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, quarkMaxResponseBytes+1))
	if err != nil {
		return result, errors.New("读取夸克响应失败")
	}
	if len(data) > quarkMaxResponseBytes {
		return result, errors.New("夸克返回数据过大，已停止处理")
	}
	if json.Unmarshal(data, &result) != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return result, quarkAPIError("HTTP", resp.StatusCode)
		}
		return result, errors.New("夸克返回格式异常")
	}
	code := quarkCode(result.Code)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if code == "" || code == "0" || code == "null" {
			code = "HTTP"
		}
		return result, quarkAPIError(code, resp.StatusCode)
	}
	if code != "0" && !(endpoint == "/account/info" && code == "OK") {
		return result, quarkAPIError(code, result.Status)
	}
	if endpoint == "/account/info" && (result.Success == nil || !*result.Success) {
		return result, ErrAuthExpired
	}
	if result.Status >= 400 {
		return result, quarkAPIError(code, result.Status)
	}
	if len(result.Data) == 0 || string(result.Data) == "null" {
		if endpoint == "/account/info" {
			return result, ErrAuthExpired
		}
		return result, errors.New("夸克响应缺少数据")
	}
	return result, nil
}

func (p *quarkProvider) Validate(ctx context.Context, credentials Credentials) (Identity, error) {
	resp, err := p.request(ctx, credentials, http.MethodGet, "/account/info", nil, nil)
	if err != nil {
		return Identity{}, err
	}
	var account struct {
		Nickname string `json:"nickname"`
	}
	if json.Unmarshal(resp.Data, &account) != nil {
		return Identity{}, errors.New("夸克账号信息格式异常")
	}
	if strings.TrimSpace(account.Nickname) == "" {
		return Identity{}, ErrAuthExpired
	}
	// A valid website session does not guarantee that the drive API accepts it.
	// Verify both services before allowing background transfers.
	if _, err := p.request(ctx, credentials, http.MethodGet, "/member", url.Values{"fetch_subscribe": {"false"}, "fetch_identity": {"false"}}, nil); err != nil {
		return Identity{}, err
	}
	id, err := quarkAccountID(credentials["cookie"])
	if err != nil {
		return Identity{}, err
	}
	return Identity{ID: id, Name: account.Nickname}, nil
}

// __uid is the stable account identifier issued with a Quark browser session.
// Read it only after both website and drive endpoints have validated the Cookie.
func quarkAccountID(cookie string) (string, error) {
	request := &http.Request{Header: http.Header{"Cookie": {cookie}}}
	id := ""
	for _, part := range request.Cookies() {
		if part.Name != "__uid" {
			continue
		}
		if id != "" || part.Value == "" || len(part.Value) > 128 {
			return "", errors.New("夸克 Cookie 账号标识无效，请重新复制完整登录凭据")
		}
		for _, c := range part.Value {
			if c <= ' ' || c >= 127 {
				return "", errors.New("夸克 Cookie 账号标识无效，请重新复制完整登录凭据")
			}
		}
		id = part.Value
	}
	if strings.Trim(id, "0") == "" {
		return "", errors.New("夸克 Cookie 缺少账号标识，请重新复制完整登录凭据")
	}
	return id, nil
}

func (p *quarkProvider) listFiles(ctx context.Context, credentials Credentials, endpoint string, query url.Values) ([]quarkFile, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("_size", strconv.Itoa(quarkPageSize))
	query.Set("_fetch_total", "1")
	query.Set("_sort", "file_type:asc,file_name:asc")
	var files []quarkFile
	seen := make(map[string]bool)
	total := -1
	for page := 1; page <= quarkMaxPages; page++ {
		query.Set("_page", strconv.Itoa(page))
		resp, err := p.request(ctx, credentials, http.MethodGet, endpoint, query, nil)
		if err != nil {
			return nil, err
		}
		var data struct {
			List json.RawMessage `json:"list"`
		}
		if json.Unmarshal(resp.Data, &data) != nil || len(data.List) == 0 || string(data.List) == "null" {
			return nil, errors.New("夸克文件列表格式异常")
		}
		var batch []quarkFile
		if json.Unmarshal(data.List, &batch) != nil {
			return nil, errors.New("夸克文件列表格式异常")
		}
		if resp.Metadata.Total != nil {
			if *resp.Metadata.Total < 0 || (total >= 0 && total != *resp.Metadata.Total) {
				return nil, errQuarkListingInconsistent
			}
			total = *resp.Metadata.Total
			if total > quarkMaxEntries {
				return nil, errors.New("夸克目录文件数量超过处理上限（10000）")
			}
		}
		for _, file := range batch {
			if file.ID == "" || seen[file.ID] {
				return nil, errors.New("夸克文件分页异常，未完成读取")
			}
			seen[file.ID] = true
			files = append(files, file)
		}
		if len(files) > quarkMaxEntries {
			return nil, errors.New("夸克目录文件数量超过处理上限（10000）")
		}
		if total >= 0 {
			if len(files) == total {
				return files, nil
			}
			if len(files) > total || len(batch) == 0 {
				return nil, errQuarkListingInconsistent
			}
		} else if len(batch) < quarkPageSize {
			return files, nil
		}
	}
	return nil, errors.New("夸克目录分页超过处理上限，未完成读取")
}

func (p *quarkProvider) ListFolders(ctx context.Context, credentials Credentials, parentID string) ([]Folder, error) {
	if parentID == "" {
		parentID = p.RootID()
	}
	files, err := p.listFiles(ctx, credentials, "/file/sort", url.Values{"pdir_fid": {parentID}, "_fetch_sub_dirs": {"0"}})
	if err != nil {
		return nil, err
	}
	folders := make([]Folder, 0)
	for _, file := range files {
		if file.isDir() {
			folders = append(folders, Folder{ID: file.ID, Name: file.Name})
		}
	}
	return folders, nil
}

func quarkShareID(link ShareLink) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(link.URL))
	if err != nil || u.Scheme != "https" || u.Hostname() != "pan.quark.cn" || u.Port() != "" || u.User != nil {
		return "", "", errors.New("请输入有效的夸克分享链接")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "s" || len(parts[1]) == 0 || len(parts[1]) > 128 {
		return "", "", errors.New("请输入有效的夸克分享链接")
	}
	for _, c := range parts[1] {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '_' && c != '-' {
			return "", "", errors.New("夸克分享标识格式不正确")
		}
	}
	password := strings.TrimSpace(link.Password)
	if password == "" {
		password = u.Query().Get("pwd")
	}
	return parts[1], password, nil
}

func quarkSafeName(name string) bool {
	return strings.TrimSpace(name) != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00\r\n")
}

func (p *quarkProvider) ReadShare(ctx context.Context, credentials Credentials, link ShareLink) (Share, error) {
	id, password, err := quarkShareID(link)
	if err != nil {
		return Share{}, err
	}
	resp, err := p.request(ctx, credentials, http.MethodPost, "/share/sharepage/token", nil, map[string]any{"pwd_id": id, "passcode": password, "support_visit_limit_private_share": true})
	if err != nil {
		return Share{}, err
	}
	var token struct {
		Value string `json:"stoken"`
	}
	if json.Unmarshal(resp.Data, &token) != nil || token.Value == "" {
		return Share{}, errors.New("夸克未返回分享访问令牌")
	}
	share := Share{ID: id, Entries: make([]Entry, 0), Data: map[string]string{"stoken": token.Value}}
	visited := map[string]bool{"0": true}
	paths := make(map[string]bool)
	var walk func(string, string, int) error
	walk = func(parentID, parentPath string, depth int) error {
		if depth > quarkMaxDepth {
			return errors.New("夸克分享目录超过 32 层，未完成读取")
		}
		files, err := p.listFiles(ctx, credentials, "/share/sharepage/detail", url.Values{"pwd_id": {id}, "stoken": {token.Value}, "pdir_fid": {parentID}, "force": {"0"}, "ver": {"2"}})
		if err != nil {
			return err
		}
		for _, file := range files {
			if !quarkSafeName(file.Name) {
				return errors.New("夸克分享包含不支持的文件名")
			}
			if visited[file.ID] {
				return errors.New("夸克分享目录存在循环或重复文件，未完成读取")
			}
			visited[file.ID] = true
			entryPath := path.Join(parentPath, file.Name)
			if paths[entryPath] {
				return errors.New("夸克分享存在重复路径，未完成读取")
			}
			paths[entryPath] = true
			entry := Entry{ID: file.ID, Name: file.Name, Path: entryPath, Token: file.Token, ParentID: parentID, Revision: file.revision(), IsDir: file.isDir(), Size: file.Size}
			if !entry.IsDir && entry.Token == "" {
				return errors.New("夸克分享文件缺少转存令牌，未完成读取")
			}
			share.Entries = append(share.Entries, entry)
			if len(share.Entries) > quarkMaxEntries {
				return errors.New("夸克分享文件数量超过处理上限（10000）")
			}
			if entry.IsDir {
				if err := walk(entry.ID, entry.Path, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk("0", "", 0); err != nil {
		return Share{}, err
	}
	return share, nil
}

func (p *quarkProvider) CreateFolder(ctx context.Context, credentials Credentials, parentID, name string) (Folder, error) {
	if !quarkSafeName(name) {
		return Folder{}, errors.New("夸克文件夹名称格式不正确")
	}
	if parentID == "" {
		parentID = p.RootID()
	}
	files, err := p.listFiles(ctx, credentials, "/file/sort", url.Values{"pdir_fid": {parentID}})
	if err != nil {
		return Folder{}, err
	}
	for _, file := range files {
		if file.Name == name {
			if file.isDir() {
				return Folder{ID: file.ID, Name: file.Name}, nil
			}
			return Folder{}, errors.New("夸克目标位置已有同名文件，无法创建目录")
		}
	}
	resp, err := p.request(ctx, credentials, http.MethodPost, "/file", nil, map[string]any{"pdir_fid": parentID, "file_name": name, "dir_path": "", "dir_init_lock": false})
	if err != nil {
		return Folder{}, err
	}
	var folder struct {
		ID string `json:"fid"`
	}
	if json.Unmarshal(resp.Data, &folder) != nil || folder.ID == "" {
		return Folder{}, errors.New("夸克未返回创建的文件夹 ID")
	}
	return Folder{ID: folder.ID, Name: name}, nil
}

func (p *quarkProvider) Save(ctx context.Context, credentials Credentials, share Share, entry Entry, destinationID string) error {
	_, err := p.SaveBatch(ctx, credentials, share, []Entry{entry}, destinationID)
	return err
}

func (p *quarkProvider) SaveBatch(ctx context.Context, credentials Credentials, share Share, entries []Entry, destinationID string) (BatchResult, error) {
	result := BatchResult{}
	ids, names, err := batchEntries(entries)
	if err != nil {
		return result, err
	}
	entry := entries[0]
	if entry.IsDir || entry.ID == "" || entry.Token == "" || share.ID == "" || share.Data["stoken"] == "" || !quarkSafeName(entry.Name) {
		return result, errors.New("夸克转存参数不完整")
	}
	if destinationID == "" {
		destinationID = p.RootID()
	}
	tokens := make([]string, 0, len(entries))
	for _, item := range entries {
		if item.Token == "" {
			return result, errors.New("夸克文件缺少转存令牌")
		}
		tokens = append(tokens, item.Token)
	}
	// Refuse a known name collision instead of overwriting or recording it as a
	// successful transfer. The caller can choose another destination explicitly.
	files, err := p.listFiles(ctx, credentials, "/file/sort", url.Values{"pdir_fid": {destinationID}})
	if err != nil {
		return result, err
	}
	for _, file := range files {
		if names[file.Name] {
			return result, ErrDestinationExists
		}
	}
	result.Submitted = true
	resp, err := p.request(ctx, credentials, http.MethodPost, "/share/sharepage/save", nil, map[string]any{
		"fid_list": ids, "fid_token_list": tokens,
		"to_pdir_fid": destinationID, "pwd_id": share.ID, "stoken": share.Data["stoken"],
		"pdir_fid": entry.ParentID, "pdir_save_all": false, "exclude_fids": []string{}, "scene": "link",
	})
	if err != nil {
		if errors.Is(err, errQuarkOwnShare) {
			result.Submitted = false
			return result, &functionFailure{"SOURCE_UNAVAILABLE", errQuarkOwnShare.Error()}
		}
		return result, err
	}
	var task struct {
		ID string `json:"task_id"`
	}
	if json.Unmarshal(resp.Data, &task) != nil || task.ID == "" {
		return result, errors.New("夸克未返回转存任务 ID，无法确认转存结果")
	}
	pollCtx, cancel := context.WithTimeout(ctx, p.taskTimeout)
	defer cancel()
	for retry := 0; ; retry++ {
		resp, err := p.request(pollCtx, credentials, http.MethodGet, "/task", url.Values{"task_id": {task.ID}, "retry_index": {strconv.Itoa(retry)}}, nil)
		if err != nil {
			if pollCtx.Err() != nil {
				return result, errors.New("夸克转存结果确认超时或已取消，请先查看网盘后再重试")
			}
			return result, err
		}
		var state struct {
			Status   *int            `json:"status"`
			FailCode json.RawMessage `json:"fail_code"`
		}
		if json.Unmarshal(resp.Data, &state) != nil || state.Status == nil {
			return result, errors.New("夸克转存任务状态格式异常，无法确认结果")
		}
		switch *state.Status {
		case 2:
			if code := quarkCode(state.FailCode); len(state.FailCode) > 0 && code != "" && code != "0" && code != "null" {
				return result, errors.New("夸克批次包含失败项，请到网盘核对")
			}
			result.Confirmed = ids
			return result, nil
		case 3:
			return result, quarkAPIError(quarkCode(state.FailCode), 200)
		case 4:
			return result, errors.New("夸克转存任务已暂停，尚未完成")
		case 0, 1:
		default:
			return result, errors.New("夸克返回未知转存任务状态，无法确认结果")
		}
		timer := time.NewTimer(p.pollInterval)
		select {
		case <-pollCtx.Done():
			timer.Stop()
			return result, errors.New("夸克转存结果确认超时或已取消，请先查看网盘后再重试")
		case <-timer.C:
		}
	}
}
