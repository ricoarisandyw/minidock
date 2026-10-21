package mcpserver

import (
	"errors"
	"sort"
	"strings"
	"unicode"
)

// CheckReadOnly rejects any SQL that is not one plain read statement, judged by
// the rules of the database it is headed for.
//
// This is the first of two layers. The second is the read-only session the query
// is run in, so the database itself refuses writes even if something slips through
// here. The check is deliberately strict: it would rather refuse a harmless query
// than let a risky one through (no comments, no backslashes, one statement).
func CheckReadOnly(d Dialect, sql string) error {
	words, err := scan(sql)
	if err != nil {
		return err
	}
	if len(words) == 0 {
		return errors.New("the query is empty")
	}
	if !d.allowedFirstWords[words[0]] {
		return errors.New(allowedText(d) + " (this server is read-only)")
	}
	if err := d.checkExtra(words, sql); err != nil {
		return err
	}
	return nil
}

// allowedText lists what this dialect does accept, for the refusal message.
func allowedText(d Dialect) string {
	words := make([]string, 0, len(d.allowedFirstWords))
	for w := range d.allowedFirstWords {
		words = append(words, strings.ToUpper(w))
	}
	sort.Strings(words)
	return "only " + strings.Join(words, ", ") + " are allowed"
}

// scan walks the SQL once, following MySQL's quoting rules. It returns the lower-case
// words found outside quotes, and fails on anything that could hide a second statement
// or a client-side command (the mysql program treats backslash commands specially).
func scan(sql string) ([]string, error) {
	rs := []rune(sql)
	var words []string
	var word []rune
	flush := func() {
		if len(word) > 0 {
			words = append(words, strings.ToLower(string(word)))
			word = word[:0]
		}
	}
	sawSemicolon := false

	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if sawSemicolon && !unicode.IsSpace(r) {
			return nil, errors.New("only one statement is allowed")
		}
		switch {
		case r == 0:
			return nil, errors.New("the query contains an invalid character")
		case r == '\'' || r == '"' || r == '`':
			flush()
			closed := false
			for i++; i < len(rs); i++ {
				c := rs[i]
				if c == '\\' && r != '`' {
					i++ // the next character is escaped
					continue
				}
				if c == r {
					if i+1 < len(rs) && rs[i+1] == r { // doubled quote stays inside the string
						i++
						continue
					}
					closed = true
					break
				}
			}
			if !closed {
				return nil, errors.New("the query has an unclosed quote")
			}
		case r == '\\':
			return nil, errors.New("backslashes outside quotes are not allowed")
		case r == '#':
			return nil, errors.New("comments are not allowed")
		case r == '-' && i+1 < len(rs) && rs[i+1] == '-':
			return nil, errors.New("comments are not allowed")
		case r == '/' && i+1 < len(rs) && rs[i+1] == '*':
			return nil, errors.New("comments are not allowed")
		case r == ';':
			flush()
			sawSemicolon = true
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$':
			word = append(word, r)
		default:
			flush()
		}
	}
	flush()
	return words, nil
}

// CheckShape refuses what no database should accept, whichever it is: more than
// one statement, comments, backslashes, unclosed quotes. It runs before the
// server is even found, so a refusal does not depend on Docker being reachable.
func CheckShape(sql string) error {
	words, err := scan(sql)
	if err != nil {
		return err
	}
	if len(words) == 0 {
		return errors.New("the query is empty")
	}
	return nil
}
