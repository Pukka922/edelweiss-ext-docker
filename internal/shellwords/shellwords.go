// Package shellwords splits a command line into words like a POSIX shell, without expansion, and joins words back.
package shellwords

import (
	"errors"
	"strings"
	"unicode"
)

// Split splits s into words: blanks separate words, '…' quotes literally, "…" quotes with \ escaping \ " $ ` and a
// newline, and a \ outside quotes escapes the next character. Nothing is expanded.
func Split(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		case r == '\\':
			if i+1 >= len(runes) {
				return nil, errors.New("ends with an unescaped backslash")
			}
			i++
			cur.WriteRune(runes[i])
			inWord = true
		case r == '\'':
			j := i + 1
			for j < len(runes) && runes[j] != '\'' {
				j++
			}
			if j >= len(runes) {
				return nil, errors.New("has an unterminated single quote")
			}
			cur.WriteString(string(runes[i+1 : j]))
			i, inWord = j, true
		case r == '"':
			j := i + 1
			for ; j < len(runes) && runes[j] != '"'; j++ {
				if runes[j] == '\\' && j+1 < len(runes) && strings.ContainsRune("\\\"$`\n", runes[j+1]) {
					j++
				}
				cur.WriteRune(runes[j])
			}
			if j >= len(runes) {
				return nil, errors.New("has an unterminated double quote")
			}
			i, inWord = j, true
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// Join joins words so that Split gives them back: a word that a shell would split or expand is single-quoted, any
// other stays as it is.
func Join(words []string) string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = quote(w)
	}
	return strings.Join(out, " ")
}

// quote single-quotes w if it is empty, has white space, a quote, a backslash or a character a shell expands or
// interprets ($ ` | & ; < > ( ) * ? [ ]), or starts with # (a comment) or ~ (home directory). Everything else, like
// --opt=value, 100%, {x} or a#b, reads the same without quotes.
func quote(w string) string {
	plain := w != "" && !strings.HasPrefix(w, "#") && !strings.HasPrefix(w, "~") &&
		strings.IndexFunc(w, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune("'\"\\$`|&;<>()*?[]", r) }) < 0
	if plain {
		return w
	}
	return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
}
