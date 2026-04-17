package buildversion

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const FormatHint = "VERSION格式必须是x.y.z.a[-αNNN]（x/y/z/a为非负整数，α为单个希腊字母或alpha/beta/gamma/delta/epsilon/rc，NNN为可选数字）"

var fixedWords = map[string]struct{}{
	"alpha":   {},
	"beta":    {},
	"gamma":   {},
	"delta":   {},
	"epsilon": {},
	"rc":      {},
}

func Validate(version string) error {
	base, suffix, hasSuffix := strings.Cut(version, "-")
	if base == "" {
		return fmt.Errorf("%s", FormatHint)
	}
	if hasSuffix && suffix == "" {
		return fmt.Errorf("%s", FormatHint)
	}

	parts := strings.Split(base, ".")
	if len(parts) != 4 {
		return fmt.Errorf("%s", FormatHint)
	}
	for _, part := range parts {
		if !isASCIIDigits(part) {
			return fmt.Errorf("%s", FormatHint)
		}
	}

	if !hasSuffix {
		return nil
	}

	label, digits := splitSuffix(suffix)
	if label == "" || (digits != "" && !isASCIIDigits(digits)) {
		return fmt.Errorf("%s", FormatHint)
	}
	if isFixedWord(label) || isSingleGreekLetter(label) {
		return nil
	}

	return fmt.Errorf("%s", FormatHint)
}

func splitSuffix(s string) (label, digits string) {
	i := len(s)
	for i > 0 {
		b := s[i-1]
		if b < '0' || b > '9' {
			break
		}
		i--
	}
	return s[:i], s[i:]
}

func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isFixedWord(s string) bool {
	_, ok := fixedWords[s]
	return ok
}

func isSingleGreekLetter(s string) bool {
	if utf8.RuneCountInString(s) != 1 {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.In(r, unicode.Greek) && unicode.IsLetter(r)
}
