package cloud

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"pansou/util/json"
	"path"
	"strconv"
	"strings"
	"time"
)

func (p *baiduProvider) checkFiles(ctx context.Context, c Credentials, parent string) ([]checkObject, error) {
	dir, err := baiduPath(parent)
	if err != nil {
		return nil, err
	}
	session, err := p.authSession(ctx, c)
	if err != nil {
		return nil, err
	}
	files, err := session.files(ctx, "/api/list", url.Values{"dir": {dir}})
	if err != nil {
		return nil, err
	}
	out := make([]checkObject, 0, len(files))
	for _, f := range files {
		out = append(out, checkObject{ID: string(f.ID), Name: f.Name, Size: int64(f.Size), IsDir: f.IsDir == 1})
	}
	return out, nil
}
func (p *quarkProvider) checkFiles(ctx context.Context, c Credentials, parent string) ([]checkObject, error) {
	var files []quarkFile
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		files, err = p.listFiles(ctx, c, "/file/sort", url.Values{"pdir_fid": {parent}, "_fetch_sub_dirs": {"0"}, "fetch_all_file": {"1"}, "fetch_risk_file_name": {"1"}, "_t": {strconv.FormatInt(time.Now().UnixNano(), 10)}})
		if !errors.Is(err, errQuarkListingInconsistent) {
			break
		}
		// After a create/delete the provider's total may lag behind its list.
		// Retry the complete read, never treat a partial listing as empty.
		if attempt < 19 {
			if waitErr := bxWait(ctx, 500*time.Millisecond); waitErr != nil {
				return nil, waitErr
			}
		}
	}
	if err != nil {
		return nil, err
	}
	out := make([]checkObject, 0, len(files))
	for _, f := range files {
		out = append(out, checkObject{ID: f.ID, Name: f.Name, Size: f.Size, IsDir: f.isDir()})
	}
	return out, nil
}
func (p *xunleiProvider) checkFiles(ctx context.Context, c Credentials, parent string) ([]checkObject, error) {
	files, err := p.files(ctx, c, parent)
	if err != nil {
		return nil, err
	}
	out := make([]checkObject, 0, len(files))
	for _, f := range files {
		size := int64(0)
		if f.Size != "" {
			if size, err = strconv.ParseInt(string(f.Size), 10, 64); err != nil {
				return nil, errors.New("迅雷文件大小异常")
			}
		}
		out = append(out, checkObject{ID: f.ID, Name: f.Name, Size: size, IsDir: f.Kind == "drive#folder"})
	}
	return out, nil
}

// Only called for a journaled check object after identity and contents checks.
// No public endpoint accepts arbitrary remote delete IDs or paths.
func checkDeleteTarget(root, parent string, obj checkObject) error {
	if obj.ID == "" || !bxName(obj.Name) {
		return errors.New("测试清理目标无效")
	}
	if obj.IsDir && (parent != root || !strings.HasPrefix(obj.Name, checkFolderPrefix)) {
		return errors.New("只允许清理本次测试目录")
	}
	if !obj.IsDir && parent == root {
		return errors.New("禁止删除网盘根目录文件")
	}
	return nil
}
func (p *quarkProvider) deleteCheckObject(ctx context.Context, c Credentials, parent string, obj checkObject) error {
	if err := checkDeleteTarget(p.RootID(), parent, obj); err != nil {
		return err
	}
	_, err := p.request(ctx, c, "POST", "/file/delete", nil, map[string]any{"action_type": 2, "filelist": []string{obj.ID}, "exclude_fids": []string{}})
	return err
}
func (p *xunleiProvider) deleteCheckObject(ctx context.Context, c Credentials, parent string, obj checkObject) error {
	if err := checkDeleteTarget(p.RootID(), parent, obj); err != nil {
		return err
	}
	if !xunleiID.MatchString(obj.ID) {
		return errors.New("迅雷清理标识无效")
	}
	var response map[string]any
	return p.request(ctx, c, "POST", "/files:batchDelete", nil, map[string]any{"ids": []string{obj.ID}}, &response)
}
func (p *baiduProvider) deleteCheckObject(ctx context.Context, c Credentials, parent string, obj checkObject) error {
	if err := checkDeleteTarget(p.RootID(), parent, obj); err != nil {
		return err
	}
	target := path.Join(parent, obj.Name)
	if !strings.HasPrefix(target, "/"+checkFolderPrefix) || path.Clean(target) != target {
		return errors.New("百度清理路径不属于测试目录")
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	encoded, _ := json.Marshal(map[string]any{"list": []map[string]string{{"path": target}}})
	if err := form.WriteField("param", string(encoded)); err != nil {
		return err
	}
	if err := form.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://pcs.baidu.com/rest/2.0/pcs/file?method=delete&app_id=266719", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Cookie", c["cookie"])
	req.Header.Set("User-Agent", "Mozilla/5.0")
	client := *p.client
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return errors.New("百度清理连接失败")
	}
	defer response.Body.Close()
	var result struct {
		Code      *int            `json:"error_code"`
		RequestID json.RawMessage `json:"request_id"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result) != nil {
		return errors.New("百度清理返回异常")
	}
	code := 0
	if result.Code != nil {
		code = *result.Code
	}
	if response.StatusCode != 200 || code != 0 {
		return fmt.Errorf("百度清理失败（HTTP %d，code %d）", response.StatusCode, code)
	}
	var requestID bxNumber
	if json.Unmarshal(result.RequestID, &requestID) != nil || requestID == "" || requestID == "0" {
		return errors.New("百度清理未返回确认信息")
	}
	return nil
}
