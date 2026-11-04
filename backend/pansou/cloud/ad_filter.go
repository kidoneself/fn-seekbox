package cloud

import (
	"path"
	"strings"
	"unicode"
)

// Deliberately limited to promotional attachments. Do not classify media,
// subtitles, archives, PDFs, or entire directories by their names.
func isAdAttachment(entry Entry) bool {
	if entry.IsDir {
		return false
	}
	ext := strings.ToLower(path.Ext(entry.Name))
	switch ext {
	case ".txt", ".url", ".webloc", ".lnk", ".html", ".htm", ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
	default:
		return false
	}
	name := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSuffix(entry.Name, path.Ext(entry.Name)))
	switch name {
	case "广告", "推广", "公众号", "二维码":
		return true
	}
	for _, word := range []string{"加群", "进群", "qq群", "资源群", "资源交流群", "扫码关注", "扫码领取", "扫码下载", "关注公众号", "推广链接", "推广二维码", "广告推广", "广告链接", "广告二维码", "更多资源", "防失联", "防失效", "最新地址", "备用网址", "备用地址"} {
		if strings.Contains(name, word) {
			return true
		}
	}
	return false
}

func filterAdAttachments(entries []Entry) ([]Entry, int) {
	filtered := 0
	kept := make([]Entry, 0, len(entries))
	needed := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		if isAdAttachment(entry) {
			filtered++
			continue
		}
		kept = append(kept, entry)
		for parent := path.Dir(entry.Path); parent != "." && parent != "/"; parent = path.Dir(parent) {
			needed[parent] = true
		}
	}
	// Do not create empty folders left by filtering. Existing cloud files and
	// folders are never removed, and excluded files receive no "done" mark.
	for _, entry := range entries {
		if entry.IsDir && needed[entry.Path] {
			kept = append(kept, entry)
		}
	}
	return kept, filtered
}
