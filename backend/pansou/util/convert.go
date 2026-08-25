package util

import (
	"strconv"
	"strings"
)

// StringToInt 将字符串转换为整数，如果转换失败则返回0
func StringToInt(s string) int {
	if s == "" {
		return 0
	}

	i, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return i
}

// CutTitleByKeywords 根据关键词裁剪标题，保留最靠前关键词之前的部分
func CutTitleByKeywords(title string, keywords []string) string {
	minIdx := -1
	for _, kw := range keywords {
		if idx := strings.Index(title, kw); idx >= 0 && (minIdx == -1 || idx < minIdx) {
			minIdx = idx
		}
	}
	if minIdx > 0 {
		return strings.TrimSpace(title[:minIdx])
	}
	return strings.TrimSpace(title)
}
