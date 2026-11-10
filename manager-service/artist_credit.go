package main

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// A compact ampersand can be part of a name. A collaboration with an explicit
// separator or several compact separators can use both forms in the same credit.
func artistCreditNames(credit string) []string {
	return splitArtistCredit(credit, true)
}

func splitArtistCredit(credit string, compactSeries bool) []string {
	credit = strings.TrimSpace(credit)
	if credit == "" {
		return nil
	}
	if !strings.ContainsAny(credit, "&＆") {
		return []string{artistCreditPart(credit)}
	}

	type separator struct{ start, end int }
	var separators []separator
	explicit := false
	start := 0
	quoted, escaped := false, false
	for i, r := range credit {
		if quoted {
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				quoted = false
			}
			continue
		}
		if r == '"' && strings.TrimSpace(credit[start:i]) == "" {
			quoted = true
			continue
		}
		if r != '&' && r != '＆' {
			continue
		}
		next := i + utf8.RuneLen(r)
		if i == 0 || next == len(credit) {
			continue
		}
		left, _ := utf8.DecodeLastRuneInString(credit[:i])
		right, _ := utf8.DecodeRuneInString(credit[next:])
		clear := r == '＆' || unicode.IsSpace(left) || unicode.IsSpace(right) ||
			artistSeparatorScript(left) || artistSeparatorScript(right)
		if !clear && (!compactSeries || literalRnB(credit, i)) {
			continue
		}
		separators = append(separators, separator{i, next})
		explicit = explicit || clear
		start = next
	}
	if !explicit && len(separators) < 2 {
		return []string{artistCreditPart(credit)}
	}
	var names []string
	seen := make(map[string]struct{}, len(separators)+1)
	parts := 0
	add := func(part string) {
		name := artistCreditPart(part)
		if name == "" || strings.Trim(name, "&＆") == "" {
			return
		}
		parts++
		if _, exists := seen[name]; !exists {
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	start = 0
	for _, cut := range separators {
		add(credit[start:cut.start])
		start = cut.end
	}
	add(credit[start:])
	if parts < 2 {
		return []string{credit}
	}
	return names
}

func literalRnB(credit string, amp int) bool {
	if !strings.EqualFold(credit[amp-1:amp+2], "R&B") {
		return false
	}
	left, _ := utf8.DecodeLastRuneInString(credit[:amp-1])
	right, _ := utf8.DecodeRuneInString(credit[amp+2:])
	boundary := func(r rune) bool { return r == utf8.RuneError || r == '&' || r == '＆' || unicode.IsSpace(r) }
	return boundary(left) && boundary(right)
}

func artistCreditPart(part string) string {
	part = strings.TrimSpace(part)
	if strings.HasPrefix(part, `"`) {
		if name, err := strconv.Unquote(part); err == nil && strings.TrimSpace(name) != "" {
			return name
		}
	}
	return part
}

// Preserve exact names obtained from native multi-value tags, even when an
// artist's literal name itself contains a collaboration separator.
func formatArtistCredit(names []string) string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if !slices.Equal(artistCreditNames(name), []string{name}) ||
			len(names) > 1 && strings.ContainsRune(name, '&') && !strings.EqualFold(name, "R&B") {
			name = strconv.Quote(name)
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, " & ")
}

func artistSeparatorScript(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

var foldIdentity = cases.Fold()

func identityText(value string) string {
	value = norm.NFC.String(strings.Join(strings.Fields(value), " "))
	return norm.NFC.String(foldIdentity.String(value))
}

// The order of collaborators is a display choice. The identity is used only
// to find candidates; it never replaces a stored tag, directory or artist ID.
func artistCreditIdentity(credit string) string {
	return artistNamesIdentity(artistCreditNames(credit))
}

func artistNamesIdentity(names []string) string {
	for i, name := range names {
		names[i] = identityText(name)
	}
	slices.Sort(names)
	var identity strings.Builder
	var previous string
	for _, name := range names {
		if name == "" || name == previous {
			continue
		}
		// Length-prefix each folded name so punctuation in a name is unambiguous.
		identity.WriteString(strconv.Itoa(len(name)))
		identity.WriteByte(':')
		identity.WriteString(name)
		previous = name
	}
	return identity.String()
}
