package cloud

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Filename inference is a highest observed marker, never a completeness claim.
// Saved is limited to files in this snapshot with a durable successful journal.
type ContentProgress struct {
	Kind     string `json:"kind"`
	Value    string `json:"value,omitempty"`
	Label    string `json:"label,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}
type FollowProgress struct {
	Source         ContentProgress `json:"source"`
	Saved          ContentProgress `json:"saved"`
	CheckedAt      int64           `json:"checked_at"`
	ConfirmedFiles int             `json:"confirmed_files"`
}

var (
	progressSE          = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])S([0-9]{1,2})[ ._-]*E(?:P)?([0-9]{1,4})(?:[^0-9]|$)`)
	progressEP          = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])EP?([0-9]{1,4})(?:[^0-9]|$)`)
	progressCN          = regexp.MustCompile(`第\s*([0-9零〇一二两三四五六七八九十百千]{1,8})\s*([集话話期])`)
	progressSeason      = regexp.MustCompile(`(?i)(?:第\s*([0-9零〇一二两三四五六七八九十百千]{1,8})\s*季|(?:^|[^a-z0-9])(?:Season\s*|S)([0-9]{1,2})(?:[^0-9]|$))`)
	progressPlain       = regexp.MustCompile(`(?i)^([0-9]{1,4})(?:[-_. ]+(?:4k|8k|hdr|高码率?|臻彩|max|杜比).*)?$`)
	progressDate        = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)[0-9]{2})[.年/-]([01]?[0-9])[.月/-]([0-3]?[0-9])(?:日|[^0-9]|$)`)
	progressCompactDate = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)[0-9]{2})([01][0-9])([0-3][0-9])(?:[^0-9]|$)`)
	progressExtra       = regexp.MustCompile(`(?i)预告|花絮|彩蛋|先导|特辑|幕后|加更|纯享|陪看|衍生|番外|制作特辑|(?:^|[^a-z])(?:trailer|sample|extras?)(?:[^a-z]|$)`)
)

func progressNumber(value string) int {
	if n, err := strconv.Atoi(value); err == nil {
		return n
	}
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	units := map[rune]int{'十': 10, '百': 100, '千': 1000}
	total, current := 0, 0
	for _, r := range value {
		if n, ok := digits[r]; ok {
			current = current*10 + n
			continue
		}
		unit := units[r]
		if unit == 0 {
			return 0
		}
		if current == 0 {
			current = 1
		}
		total += current * unit
		current = 0
	}
	return total + current
}

type progressCandidate struct {
	season, number, part int
	kind, evidence       string
}

func (c progressCandidate) greater(other progressCandidate) bool {
	if c.season != other.season {
		return c.season > other.season
	}
	if c.number != other.number {
		return c.number > other.number
	}
	if c.part != other.part {
		return c.part > other.part
	}
	return c.evidence < other.evidence
}

// Similar to 影视盘: use media filenames, exclude extras, and never use the
// number of files or a search result title as an episode number.
func analyzeProgress(entries []Entry) ContentProgress {
	best := map[string]progressCandidate{}
	dates := map[string]string{}
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		name := e.Name
		if name == "" {
			name = path.Base(e.Path)
		}
		ext := strings.ToLower(path.Ext(name))
		if !strings.Contains("|.mp4|.mkv|.avi|.mov|.wmv|.flv|.ts|.m2ts|.rmvb|.webm|", "|"+ext+"|") || ext == "" {
			continue
		}
		if progressExtra.MatchString(e.Path + "/" + name) {
			continue
		}
		stem := strings.TrimSuffix(name, path.Ext(name))
		evidence := e.Path
		if evidence == "" {
			evidence = name
		}
		c := progressCandidate{kind: "episode", evidence: evidence}
		if m := progressSeason.FindStringSubmatch(evidence); m != nil {
			c.season = progressNumber(m[1])
			if c.season == 0 {
				c.season = progressNumber(m[2])
			}
		}
		if m := progressSE.FindStringSubmatch(stem); m != nil {
			c.season, c.number = progressNumber(m[1]), progressNumber(m[2])
		} else if m := progressCN.FindStringSubmatch(stem); m != nil {
			c.number = progressNumber(m[1])
			if m[2] == "期" {
				c.kind = "issue"
				tail := strings.TrimLeft(strings.SplitN(stem, m[0], 2)[1], " （([【")
				for i, part := range []string{"上", "中", "下"} {
					if strings.HasPrefix(tail, part) {
						c.part = i + 1
					}
				}
			}
		} else if m := progressEP.FindStringSubmatch(stem); m != nil {
			c.number = progressNumber(m[1])
		} else if m := progressPlain.FindStringSubmatch(strings.TrimSpace(stem)); m != nil {
			c.number = progressNumber(m[1])
			// Bare years/resolutions are too ambiguous, even in a mixed share.
			if c.number >= 1900 && c.number <= 2099 {
				c.number = 0
			}
			switch c.number {
			case 480, 576, 720, 1080, 1440, 2160, 4320:
				c.number = 0
			}
		}
		if c.number > 0 && c.number < 10000 && (best[c.kind].number == 0 || c.greater(best[c.kind])) {
			best[c.kind] = c
		}
		dateMatches := append(progressDate.FindAllStringSubmatch(name, -1), progressCompactDate.FindAllStringSubmatch(name, -1)...)
		for _, m := range dateMatches {
			y, mn, d := progressNumber(m[1]), progressNumber(m[2]), progressNumber(m[3])
			date := time.Date(y, time.Month(mn), d, 0, 0, 0, 0, time.UTC)
			if date.Year() == y && int(date.Month()) == mn && date.Day() == d {
				key := date.Format("2006-01-02")
				if dates[key] == "" || evidence < dates[key] {
					dates[key] = evidence
				}
			}
		}
	}
	// Mixed episode/issue collections cannot reliably be described by one number.
	if len(best) > 1 {
		return ContentProgress{Kind: "unknown"}
	}
	for _, kind := range []string{"issue", "episode"} {
		if c, ok := best[kind]; ok {
			unit := "集"
			if kind == "issue" {
				unit = "期"
			}
			label := fmt.Sprintf("第%d%s", c.number, unit)
			if c.season > 0 {
				label = fmt.Sprintf("第%d季 · ", c.season) + label
			}
			if c.part > 0 {
				label += "（" + []string{"上", "中", "下"}[c.part-1] + "）"
			}
			return ContentProgress{Kind: kind, Value: fmt.Sprintf("%d:%d:%d", c.season, c.number, c.part), Label: label, Evidence: c.evidence}
		}
	}
	// A single movie release date is not update progress.
	if len(dates) >= 2 {
		keys := make([]string, 0, len(dates))
		for k := range dates {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		last := keys[len(keys)-1]
		return ContentProgress{Kind: "date", Value: last, Label: last, Evidence: dates[last]}
	}
	return ContentProgress{Kind: "unknown"}
}

func (s *store) followProgress(owner string, job jobRecord, share Share, checkedAt int64) (*FollowProgress, error) {
	confirmed := []Entry{}
	err := s.db.View(func(tx *bolt.Tx) error {
		for _, e := range share.Entries {
			if !e.IsDir && string(tx.Bucket(marksBucket).Get([]byte(transferMark(owner, job, share, e)))) == "done" {
				confirmed = append(confirmed, e)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &FollowProgress{Source: analyzeProgress(share.Entries), Saved: analyzeProgress(confirmed), CheckedAt: checkedAt, ConfirmedFiles: len(confirmed)}, nil
}
