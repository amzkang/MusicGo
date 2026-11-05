package lyric

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// LX word markers are <offset,duration> in milliseconds relative to the line.
// Source: https://github.com/lyswhut/lx-music-desktop/blob/master/src/common/utils/lyric-font-player/font-player.js
var lxWordTag = regexp.MustCompile(`<(-?\d+),(-?\d+)>`)
var enhancedWordTag = regexp.MustCompile(`<(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?>`)

func parseWords(text string, lineTime, offset int64) (string, []Word) {
	tag := lxWordTag
	matches := tag.FindAllStringSubmatchIndex(text, -1)
	absolute := false
	if len(matches) == 0 {
		tag = enhancedWordTag
		matches = tag.FindAllStringSubmatchIndex(text, -1)
		absolute = true
	}
	if len(matches) == 0 {
		return text, nil
	}
	clean := strings.TrimSpace(tag.ReplaceAllString(text, ""))
	// Partially timed text cannot truthfully be rendered as word-synced text.
	if strings.TrimSpace(text[:matches[0][0]]) != "" {
		return clean, nil
	}
	var words []Word
	previous := int64(-1)
	for i, m := range matches {
		start, err := strconv.ParseInt(text[m[2]:m[3]], 10, 64)
		duration := int64(0)
		if absolute {
			sec, e := strconv.ParseInt(text[m[4]:m[5]], 10, 64)
			if e != nil || sec >= 60 {
				return clean, nil
			}
			frac := ""
			if m[6] >= 0 {
				frac = text[m[6]:m[7]]
			}
			millis, _ := strconv.ParseInt((frac + "000")[:3], 10, 64)
			start = start*60000 + sec*1000 + millis + offset
		} else {
			var e error
			duration, e = strconv.ParseInt(text[m[4]:m[5]], 10, 64)
			if e != nil || duration < 0 || start < 0 || start > 86400000 || duration > 86400000 {
				return clean, nil
			}
			start += lineTime
		}
		if err != nil || i > 0 && start < previous {
			return clean, nil
		}
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		content := text[m[1]:end]
		if absolute && len(words) > 0 {
			words[len(words)-1].DurationMs = start - previous
		}
		previous = start
		if content != "" {
			words = append(words, Word{TimeMs: start, DurationMs: duration, Text: content})
		}
	}
	if len(words) > 0 {
		words[0].Text = strings.TrimLeftFunc(words[0].Text, unicode.IsSpace)
		words[len(words)-1].Text = strings.TrimRightFunc(words[len(words)-1].Text, unicode.IsSpace)
	}
	return clean, words
}

// ParseSources keeps ordinary LRC lines when word timing covers only part of a
// song. Unknown/invalid timing falls back to the original line, not guessed time.
func ParseSources(raw, translation, timed string) *ParsedLyric {
	result := Parse(raw)
	if strings.TrimSpace(timed) != "" {
		wordLyric := Parse(timed)
		inheritOffset(wordLyric, result)
		if len(result.Lines) == 0 {
			result = wordLyric
		} else {
			for _, line := range wordLyric.Lines {
				if len(line.Words) == 0 {
					continue
				}
				index := sort.Search(len(result.Lines), func(i int) bool { return result.Lines[i].TimeMs >= line.TimeMs-500 })
				match := -1
				near := false
				for i := index; i < len(result.Lines) && result.Lines[i].TimeMs <= line.TimeMs+500; i++ {
					near = true
					if comparableText(result.Lines[i].Text) == comparableText(line.Text) {
						match = i
						break
					}
				}
				if match >= 0 {
					result.Lines[match].Text = line.Text
					result.Lines[match].Words = line.Words
				} else if !near {
					result.Lines = append(result.Lines, line)
					sort.SliceStable(result.Lines, func(i, j int) bool { return result.Lines[i].TimeMs < result.Lines[j].TimeMs })
				}
			}
		}
	}
	attachTranslation(result, translation)
	return result
}

func comparableText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

func inheritOffset(target, source *ParsedLyric) {
	if _, ok := target.Meta["offset"]; ok || source.OffsetMs == 0 {
		return
	}
	target.OffsetMs = source.OffsetMs
	target.Meta["offset"] = strconv.FormatInt(source.OffsetMs, 10)
	for i := range target.Lines {
		target.Lines[i].TimeMs += source.OffsetMs
		for j := range target.Lines[i].Words {
			target.Lines[i].Words[j].TimeMs += source.OffsetMs
		}
	}
}

func attachTranslation(result *ParsedLyric, raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	translated := Parse(raw)
	inheritOffset(translated, result)
	for i := range result.Lines {
		// Match timestamps, so a missing translation never shifts every later line.
		at := sort.Search(len(translated.Lines), func(j int) bool { return translated.Lines[j].TimeMs >= result.Lines[i].TimeMs-50 })
		if at < len(translated.Lines) && translated.Lines[at].TimeMs <= result.Lines[i].TimeMs+50 {
			result.Lines[i].Translation = translated.Lines[at].Text
		}
	}
}

func (p *ParsedLyric) HasWordTiming() bool {
	if p != nil {
		for _, line := range p.Lines {
			if len(line.Words) > 0 {
				return true
			}
		}
	}
	return false
}
