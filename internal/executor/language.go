package executor

import (
	"strings"
)

// NormalizeLanguage returns the standard name for the input progrmaming language
func NormalizeLanguage(lang string) (name string, found bool) {
	found = true
	lang = strings.ToLower(lang)

	languageMap := map[string]string{
		"js":          "Javascript",
		"jscript":     "Javascript",
		"javscript":   "Javascript",
		"javsscript":  "Javascript",
		"javascipt":   "Javascript",
		"javasript":   "Javascript",
		"javascript":  "Javascript",
		"java script": "Javascript",
		"jscipt":      "Javascript",

		"python":  "Python",
		"pyt":     "Python",
		"pyn":     "Python",
		"pythn":   "Python",
		"phyton":  "Python",
		"py":      "Python",
		"py thon": "Python",
		"pthon":   "Python",

		"go":      "Golang",
		"golang":  "Golang",
		"gol":     "Golang",
		"goo":     "Golang",
		"g o":     "Golang",
		"golangg": "Golang",
	}

	if normalized, ok := languageMap[lang]; ok {
		return normalized, found
	}

	return lang, !found
}
