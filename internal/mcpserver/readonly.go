package mcpserver

import (
	"errors"
	"strings"
	"unicode"
)

// allowedFirstWords are the only statements the query tool accepts.
var allowedFirstWords = map[string]bool{
	"select": true, "show": true, "describe": true, "desc": true, "explain": true,
}

// CheckReadOnly rejects any SQL that is not one plain read statement.
//
// This is the first of two layers. The second is the read-only session the query
// is run in, so MySQL itself refuses writes even if something slips through here.
// The check is deliberately strict: it would rather refuse a harmless query than
// let a risky one through (no comments, no backslashes, no INTO, one statement).
func CheckReadOnly(sql string) error {
	words, err := scan(sql)
	if err != nil {
		return err
	}
	if len(words) == 0 {
		return errors.New("the query is empty")
	}
	if !allowedFirstWords[words[0]] {
		return errors.New("only SELECT, SHOW, DESCRIBE and EXPLAIN are allowed (this server is read-only)")
	}
	for i, w := range words {
		switch w {
		case "into":
			return errors.New("INTO is not allowed (it can write files or variables)")
		case "load_file":
			return errors.New("LOAD_FILE is not allowed")
		case "analyze":
			return errors.New("EXPLAIN ANALYZE is not allowed because it runs the query")
		case "for":
			if i+1 < len(words) && (words[i+1] == "update" || words[i+1] == "share") {
				return errors.New("locking reads (FOR UPDATE / FOR SHARE) are not allowed")
			}
		case "lock":
			if i+1 < len(words) && words[i+1] == "in" {
				return errors.New("locking reads (LOCK IN SHARE MODE) are not allowed")
			}
		}
	}
	return nil
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
