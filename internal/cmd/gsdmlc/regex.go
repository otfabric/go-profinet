// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"regexp"
	"strings"
)

// translatePattern turns an XML Schema 1.0 regular expression (Appendix
// F) into an anchored RE2 expression with the same language, or fails on
// a construct it cannot translate exactly. The differences handled:
// XML Schema expressions are implicitly anchored; "^" and "$" are
// ordinary characters; "." excludes only \n and \r; \d, \w, \s, \i and \c
// have the XML Schema classes; character class subtraction is refused.
func translatePattern(x string) (string, error) {
	var b strings.Builder
	rs := []rune(x)
	inClass := 0
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\\':
			if i+1 >= len(rs) {
				return "", fmt.Errorf("pattern %q: trailing backslash", x)
			}
			i++
			c := rs[i]
			cls, err := escapeClass(c, inClass > 0, rs, &i, x)
			if err != nil {
				return "", err
			}
			b.WriteString(cls)
		case inClass > 0 && r == '-' && i+1 < len(rs) && rs[i+1] == '[':
			return "", fmt.Errorf("pattern %q: character class subtraction is not implemented", x)
		case r == '[':
			if inClass > 0 {
				return "", fmt.Errorf("pattern %q: nested character class", x)
			}
			inClass++
			b.WriteRune(r)
			if i+1 < len(rs) && rs[i+1] == '^' {
				b.WriteRune('^')
				i++
			}
		case r == ']' && inClass > 0:
			inClass--
			b.WriteRune(r)
		case inClass > 0:
			if r == '[' {
				b.WriteString(`\[`)
			} else {
				b.WriteRune(r)
			}
		case r == '.':
			b.WriteString(`[^\n\r]`)
		case r == '^' || r == '$':
			b.WriteString(`\` + string(r))
		case r == '(':
			if i+1 < len(rs) && rs[i+1] == '?' {
				return "", fmt.Errorf("pattern %q: (? is not XML Schema syntax", x)
			}
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	if inClass > 0 {
		return "", fmt.Errorf("pattern %q: unterminated character class", x)
	}
	out := "^(?:" + b.String() + ")$"
	if _, err := regexp.Compile(out); err != nil {
		return "", fmt.Errorf("pattern %q: translation %q does not compile: %v", x, out, err)
	}
	return out, nil
}

const (
	nameStart = `:A-Z_a-z\x{C0}-\x{D6}\x{D8}-\x{F6}\x{F8}-\x{2FF}\x{370}-\x{37D}\x{37F}-\x{1FFF}\x{200C}-\x{200D}\x{2070}-\x{218F}\x{2C00}-\x{2FEF}\x{3001}-\x{D7FF}\x{F900}-\x{FDCF}\x{FDF0}-\x{FFFD}\x{10000}-\x{EFFFF}`
	nameChar  = nameStart + `\-.0-9\x{B7}\x{300}-\x{36F}\x{203F}-\x{2040}`
)

func escapeClass(c rune, inClass bool, rs []rune, i *int, x string) (string, error) {
	wrap := func(s string) string {
		if inClass {
			return s
		}
		return "[" + s + "]"
	}
	switch c {
	case 'n':
		return `\n`, nil
	case 'r':
		return `\r`, nil
	case 't':
		return `\t`, nil
	case '\\', '|', '.', '-', '^', '?', '*', '+', '{', '}', '(', ')', '[', ']':
		return `\` + string(c), nil
	case 'd':
		return `\p{Nd}`, nil
	case 'D':
		return `\P{Nd}`, nil
	case 's':
		return wrap(` \t\n\r`), nil
	case 'S':
		if inClass {
			return "", fmt.Errorf("pattern %q: \\S inside a class is not implemented", x)
		}
		return `[^ \t\n\r]`, nil
	case 'w':
		if inClass {
			return "", fmt.Errorf("pattern %q: \\w inside a class is not implemented", x)
		}
		return `[^\p{P}\p{Z}\p{C}]`, nil
	case 'W':
		return wrap(`\p{P}\p{Z}\p{C}`), nil
	case 'i':
		return wrap(nameStart), nil
	case 'c':
		return wrap(nameChar), nil
	case 'I', 'C':
		if inClass {
			return "", fmt.Errorf("pattern %q: \\%c inside a class is not implemented", x, c)
		}
		if c == 'I' {
			return "[^" + nameStart + "]", nil
		}
		return "[^" + nameChar + "]", nil
	case 'p', 'P':
		end := -1
		for j := *i + 1; j < len(rs); j++ {
			if rs[j] == '}' {
				end = j
				break
			}
		}
		if *i+1 >= len(rs) || rs[*i+1] != '{' || end < 0 {
			return "", fmt.Errorf("pattern %q: malformed \\%c", x, c)
		}
		name := string(rs[*i+2 : end])
		if strings.HasPrefix(name, "Is") {
			return "", fmt.Errorf("pattern %q: block escape \\p{%s} is not implemented", x, name)
		}
		*i = end
		return `\` + string(c) + "{" + name + "}", nil
	}
	return "", fmt.Errorf("pattern %q: unknown escape \\%c", x, c)
}
