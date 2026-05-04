package core

// LocalizedString maps language tag (e.g. "zh-TW", "zh-CN", "en") to the
// localized string. Use Get() to resolve with fallback chain.
type LocalizedString map[string]string

// Get returns the value for the given language with a fallback chain that
// avoids mixing scripts:
//
//   zh-CN → en → zh-TW → first non-empty entry → ""
//   zh-TW → en → first non-empty → ""
//   en    → first non-empty → ""
//   other → matching entry, else en, else first non-empty, else ""
//
// The zh-CN-prefers-en rule prevents traditional-Chinese fallback when the
// user has chosen simplified Chinese (mixing 崩壞 and 崩坏 in one sidebar
// is jarring; English is a cleaner fallback than wrong-script Chinese).
func (l LocalizedString) Get(lang string) string {
	if v, ok := l[lang]; ok && v != "" {
		return v
	}
	switch lang {
	case "zh-CN":
		if v, ok := l["en"]; ok && v != "" {
			return v
		}
		if v, ok := l["zh-TW"]; ok && v != "" {
			return v
		}
	case "zh-TW":
		if v, ok := l["en"]; ok && v != "" {
			return v
		}
	default:
		if v, ok := l["en"]; ok && v != "" {
			return v
		}
	}
	for _, v := range l {
		if v != "" {
			return v
		}
	}
	return ""
}
