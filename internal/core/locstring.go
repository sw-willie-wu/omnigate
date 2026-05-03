package core

// LocalizedString maps language tag (e.g. "zh-TW", "en") to the localized
// string. Use Get() to resolve with English fallback.
type LocalizedString map[string]string

// Get returns the value for the given language. Falls back to "en" if missing,
// then to empty string.
func (l LocalizedString) Get(lang string) string {
	if v, ok := l[lang]; ok {
		return v
	}
	if v, ok := l["en"]; ok {
		return v
	}
	return ""
}
