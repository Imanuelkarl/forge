// Package naming converts identifiers between snake, kebab, camel and Pascal case.
package naming

import (
	"strings"
	"unicode"
)

var initialisms = map[string]string{
	"id": "ID", "url": "URL", "uuid": "UUID", "api": "API", "ip": "IP",
	"http": "HTTP", "json": "JSON", "sql": "SQL", "uri": "URI",
}

// Words splits an identifier written in snake, kebab, space or camel case into lowercase words.
func Words(s string) []string {
	var words []string
	var cur []rune

	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}

	runes := []rune(s)
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || r == ' ' || r == '.':
			flush()
		case unicode.IsUpper(r):
			if i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
				flush()
			} else if i > 0 && i+1 < len(runes) && unicode.IsUpper(runes[i-1]) && unicode.IsLower(runes[i+1]) {
				flush()
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

func titleWord(w string) string {
	if v, ok := initialisms[w]; ok {
		return v
	}
	if w == "" {
		return w
	}
	return strings.ToUpper(w[:1]) + w[1:]
}

// Pascal returns PascalCase with Go initialisms (customer_id -> CustomerID).
func Pascal(s string) string {
	var b strings.Builder
	for _, w := range Words(s) {
		b.WriteString(titleWord(w))
	}
	return b.String()
}

// Camel returns camelCase (order_item -> orderItem).
func Camel(s string) string {
	ws := Words(s)
	if len(ws) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(ws[0])
	for _, w := range ws[1:] {
		b.WriteString(titleWord(w))
	}
	return b.String()
}

// Snake returns snake_case.
func Snake(s string) string { return strings.Join(Words(s), "_") }

// Kebab returns kebab-case.
func Kebab(s string) string { return strings.Join(Words(s), "-") }

// Human returns lowercase words separated by spaces, for messages and docs.
func Human(s string) string { return strings.Join(Words(s), " ") }

// Plural pluralizes the last word of a snake_case identifier using simple English rules.
// Use the `plural:` key in forge.yaml for irregular nouns.
func Plural(s string) string {
	switch {
	case s == "":
		return s
	case strings.HasSuffix(s, "y") && len(s) > 1 && !strings.ContainsRune("aeiou", rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "z"),
		strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	default:
		return s + "s"
	}
}
