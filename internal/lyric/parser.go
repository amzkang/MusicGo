package lyric

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Line represents a single parsed lyric line
type Line struct {
	TimeMs      int64  `json:"timeMs"`
	Text        string `json:"text"`
	Translation string `json:"translation,omitempty"`
	Words       []Word `json:"words,omitempty"`
}

// Word is a source-provided syllable or word, timed against the audio clock.
type Word struct {
	TimeMs     int64  `json:"timeMs"`
	DurationMs int64  `json:"durationMs"`
	Text       string `json:"text"`
}

// Progress uses the supplied duration, including sustained notes and silence.
// A timestamp without a duration switches on at its start; it is never spread
// across the next line or divided evenly among characters.
func (w Word) Progress(position float64) float32 {
	elapsed := position*1000 - float64(w.TimeMs)
	if elapsed < 0 {
		return 0
	}
	if w.DurationMs <= 0 {
		return 1
	}
	return float32(min(elapsed/float64(w.DurationMs), 1))
}

// ParsedLyric represents the parsed lyric structure
type ParsedLyric struct {
	Title    string            `json:"title,omitempty"`
	Artist   string            `json:"artist,omitempty"`
	Album    string            `json:"album,omitempty"`
	By       string            `json:"by,omitempty"`
	OffsetMs int64             `json:"offsetMs,omitempty"`
	Lines    []Line            `json:"lines"`
	Meta     map[string]string `json:"meta,omitempty"`
}

var (
	timeTagRegex = regexp.MustCompile(`\[(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?\]`)
	metaTagRegex = regexp.MustCompile(`\[([a-zA-Z]+):([^\]]*)\]`)
)

// Parse parses a raw LRC text string into a ParsedLyric
func Parse(rawLrc string) *ParsedLyric {
	return ParseWithTranslation(rawLrc, "")
}

// ParseWithTranslation parses a raw LRC text string, and puts the
// translation that goes with it under the lines it belongs to.
func ParseWithTranslation(rawLrc, translation string) *ParsedLyric {
	res := &ParsedLyric{
		Lines: make([]Line, 0),
		Meta:  make(map[string]string),
	}

	lines := strings.Split(strings.ReplaceAll(rawLrc, "\r\n", "\n"), "\n")

	// The metadata is read first, so the offset it holds applies to every
	// line, even the ones that come before the tag.
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || timeTagRegex.MatchString(line) {
			continue
		}
		metaMatches := metaTagRegex.FindStringSubmatch(line)
		if len(metaMatches) != 3 {
			continue
		}
		tag := strings.ToLower(metaMatches[1])
		val := strings.TrimSpace(metaMatches[2])
		res.Meta[tag] = val
		switch tag {
		case "ti":
			res.Title = val
		case "ar":
			res.Artist = val
		case "al":
			res.Album = val
		case "by":
			res.By = val
		case "offset":
			if off, err := strconv.ParseInt(strings.TrimPrefix(val, "+"), 10, 64); err == nil {
				res.OffsetMs = off
			}
		}
	}

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		// Find all time tags on the line
		timeMatches := timeTagRegex.FindAllStringSubmatch(line, -1)
		if len(timeMatches) == 0 {
			continue
		}

		// Lyric text is whatever remains after removing all time tags
		text := timeTagRegex.ReplaceAllString(line, "")
		text = strings.TrimSpace(text)

		for _, match := range timeMatches {
			min, _ := strconv.ParseInt(match[1], 10, 64)
			sec, _ := strconv.ParseInt(match[2], 10, 64)
			var ms int64
			if len(match) > 3 && match[3] != "" {
				frac := match[3]
				switch len(frac) {
				case 1:
					frac += "00"
				case 2:
					frac += "0"
				case 3:
				default:
					frac = frac[:3]
				}
				ms, _ = strconv.ParseInt(frac, 10, 64)
			}
			totalMs := (min*60+sec)*1000 + ms + res.OffsetMs
			clean, words := parseWords(text, totalMs, res.OffsetMs)
			res.Lines = append(res.Lines, Line{
				TimeMs: totalMs,
				Text:   clean,
				Words:  words,
			})
		}
	}

	// Sort lines by TimeMs
	sort.SliceStable(res.Lines, func(i, j int) bool {
		return res.Lines[i].TimeMs < res.Lines[j].TimeMs
	})

	attachTranslation(res, translation)

	return res
}

// FindCurrentIndex returns the index of the line active at the given timestamp in seconds
func (p *ParsedLyric) FindCurrentIndex(currentSec float64) int {
	if len(p.Lines) == 0 {
		return -1
	}

	currentMs := int64(currentSec * 1000)
	if currentMs < p.Lines[0].TimeMs {
		return 0
	}

	idx := sort.Search(len(p.Lines), func(i int) bool {
		return p.Lines[i].TimeMs > currentMs
	})

	// sort.Search returns the first index where TimeMs > currentMs
	// Therefore the active line is idx - 1
	if idx > 0 {
		return idx - 1
	}
	return 0
}

// GetLine returns the line at index, or nil if out of bounds
func (p *ParsedLyric) GetLine(idx int) *Line {
	if idx < 0 || idx >= len(p.Lines) {
		return nil
	}
	return &p.Lines[idx]
}
