package aipan

import (
	"strings"
	"testing"
)

func TestConvertLinks(t *testing.T) {
	detail := detailResponse{
		PanLinks: []panLink{
			{Kind: "quark", URL: "https://pan.quark.cn/s/demo"},
			{Kind: "guangya", URL: "https://www.guangyapan.com/s/abc"},
			{Kind: "alipan", URL: "https://www.aliyundrive.com/s/demo?pwd=Ab12"},
		},
		Resources: []resource{
			{Kind: "magnet", URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", Name: "测试资源"},
			{Kind: "ed2k", URL: "ed2k://|file|demo.mkv|123|0123456789abcdef0123456789abcdef|/", Name: "电驴"},
		},
	}
	links := convertLinks(detail)
	if len(links) != 5 {
		t.Fatalf("got %d links: %+v", len(links), links)
	}
	if links[0].Type != "quark" || links[1].Type != "guangya" || links[2].Type != "aliyun" || links[2].Password != "Ab12" {
		t.Fatalf("unexpected pan links: %+v", links)
	}
	if links[3].Type != "magnet" || links[4].Type != "ed2k" {
		t.Fatalf("unexpected torrent links: %+v", links)
	}
}

func TestDetailContent(t *testing.T) {
	detail := detailResponse{
		Aka: "The Immortal Ascension", Year: 2025, Region: "中国大陆",
		Genres: []string{"奇幻", "古装"}, Directors: []string{"杨阳"},
		Actors: []string{"杨洋", "金晨"}, Synopsis: "简介内容",
		Resources: []resource{{Name: "凡人修仙传 4K", SizeLabel: "50G"}},
	}
	content := detailContent(detail)
	for _, want := range []string{"别名：The Immortal Ascension", "年份：2025", "导演：杨阳", "资源：凡人修仙传 4K [50G]"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content %q does not contain %q", content, want)
		}
	}
}

func TestLinkTypeByURL(t *testing.T) {
	cases := map[string]string{
		"https://pan.baidu.com/s/demo":                                 "baidu",
		"https://drive.uc.cn/s/demo":                                   "uc",
		"https://pan.xunlei.com/s/demo":                                "xunlei",
		"https://cloud.189.cn/t/demo":                                  "tianyi",
		"https://www.guangyapan.com/s/demo":                            "guangya",
		"https://www.aliyundrive.com/s/demo":                           "aliyun",
		"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567": "magnet",
	}
	for raw, want := range cases {
		if got := linkType("", raw); got != want {
			t.Errorf("linkType(%q)=%q, want %q", raw, got, want)
		}
	}
}
