package lyric

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxKRCBytes = 2 << 20
const maxKRCTextBytes = 4 << 20

var krcLineTag = regexp.MustCompile(`^\[(\d+),(\d+)\](.*)$`)
var krcWordTag = regexp.MustCompile(`<(-?\d+),(-?\d+),(-?\d+)>`)

// DecodeKRC converts the API's Base64 KRC payload into plain LRC plus LX word
// timing. It runs in the plugin worker, using only Go's standard library.
func DecodeKRC(encoded string) (raw, timed string, err error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(maxKRCBytes) {
		return "", "", fmt.Errorf("KRC payload too large")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", fmt.Errorf("invalid KRC encoding: %w", err)
	}
	if len(data) > maxKRCBytes {
		return "", "", fmt.Errorf("KRC payload too large")
	}
	if len(data) <= 4 || !bytes.Equal(data[:4], []byte("krc1")) {
		return "", "", fmt.Errorf("invalid KRC header")
	}
	key := [16]byte{64, 71, 97, 119, 94, 50, 116, 71, 81, 54, 49, 45, 206, 210, 110, 105}
	for i := 4; i < len(data); i++ {
		data[i] ^= key[(i-4)%len(key)]
	}
	zr, err := zlib.NewReader(bytes.NewReader(data[4:]))
	if err != nil {
		return "", "", fmt.Errorf("invalid KRC compression: %w", err)
	}
	defer zr.Close()
	text, err := io.ReadAll(io.LimitReader(zr, maxKRCTextBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("decode KRC: %w", err)
	}
	if len(text) > maxKRCTextBytes || !utf8.Valid(text) {
		return "", "", fmt.Errorf("invalid KRC text")
	}
	var plain, words strings.Builder
	hasWords := false
	for _, row := range strings.Split(strings.TrimPrefix(string(text), "\ufeff"), "\n") {
		row = strings.TrimSpace(row)
		match := krcLineTag.FindStringSubmatch(row)
		if match == nil {
			if meta := metaTagRegex.FindStringSubmatch(row); len(meta) == 3 {
				switch strings.ToLower(meta[1]) {
				case "ti", "ar", "al", "by", "offset":
					plain.WriteString(row + "\n")
					words.WriteString(row + "\n")
				}
			}
			continue
		}
		start, e := strconv.ParseInt(match[1], 10, 64)
		duration, de := strconv.ParseInt(match[2], 10, 64)
		if e != nil || de != nil || start > 86400000 || duration > 86400000 {
			return "", "", fmt.Errorf("invalid KRC line time")
		}
		stamp := fmt.Sprintf("[%02d:%02d.%03d]", start/60000, start/1000%60, start%1000)
		clean := strings.TrimSpace(krcWordTag.ReplaceAllString(match[3], ""))
		lx := krcWordTag.ReplaceAllString(match[3], "<$1,$2>")
		_, parsed := parseWords(lx, start, 0)
		if len(parsed) > 0 {
			hasWords = true
		} else {
			lx = clean
		}
		plain.WriteString(stamp + clean + "\n")
		words.WriteString(stamp + lx + "\n")
	}
	if !hasWords {
		return "", "", fmt.Errorf("KRC contains no usable word timing")
	}
	return html.UnescapeString(plain.String()), html.UnescapeString(words.String()), nil
}
