package gachaicon

import (
	"bufio"
	_ "embed"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// htmlTagRe matches inline markup that Project Amber/Yatta embed in some display
// names (e.g. HSR "銀狼LV.<unbreak>999</unbreak>"). The gacha record API returns
// the clean text ("銀狼LV.999"), so the tags must be stripped before keying or the
// names never match. No real item name contains angle brackets, so this is safe.
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

//go:embed data/TSCharacters.txt
var tsCharactersTxt string

// t2sTable maps one Traditional rune → its default Simplified rune (first token).
var t2sTable = buildT2S(tsCharactersTxt)

func buildT2S(data string) map[rune]rune {
	m := make(map[rune]rune, 4096)
	sc := bufio.NewScanner(strings.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sep := strings.IndexAny(line, "\t ")
		if sep <= 0 {
			continue
		}
		key := line[:sep]
		rest := strings.TrimSpace(line[sep:])
		if key == "" || rest == "" {
			continue
		}
		kr, _ := utf8.DecodeRuneInString(key)
		firstTok := rest
		if i := strings.IndexAny(rest, " \t"); i >= 0 {
			firstTok = rest[:i]
		}
		// TSCharacters candidates are always single-char; we take only the first
		// rune of the default (first) token. A multi-rune token would be truncated,
		// which cannot happen with this table.
		vr, _ := utf8.DecodeRuneInString(firstTok)
		if utf8.RuneCountInString(key) == 1 && kr != utf8.RuneError && vr != utf8.RuneError {
			m[kr] = vr
		}
	}
	return m
}

// t2s converts Traditional Chinese to Simplified, char by char (default mapping).
// Non-mapped runes (ASCII, digits, already-simplified) pass through unchanged.
func t2s(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if v, ok := t2sTable[r]; ok {
			b.WriteRune(v)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Canonicalize strips inline markup, trims, and NFC-normalizes a name for stable
// map keying — so a marked-up dataset name and the clean record name key alike.
func Canonicalize(s string) string {
	return norm.NFC.String(strings.TrimSpace(htmlTagRe.ReplaceAllString(s, "")))
}
