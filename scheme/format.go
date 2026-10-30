package scheme

import "strings"

// Format returns src re-indented: each line's leading whitespace becomes the
// column of the innermost parenthesis still open at the line's start, plus two
// spaces — or, for a line that begins with a close parenthesis, that
// parenthesis's own column. Comments, strings, raw strings and block comments
// are copied as they are, and only leading whitespace ever changes, so
// formatting twice gives the same bytes. It accepts exactly what Read accepts.
func Format(src string) (string, error) {
	if _, err := Read(src); err != nil {
		return "", err
	}
	var b strings.Builder
	var open []int // columns of the '(' still open
	inString, inRaw := false, false
	inBlock := 0
	lines := strings.Split(src, "\n")
	for li, line := range lines {
		body := strings.TrimRight(line, " \t\r")
		switch {
		case inString || inRaw || inBlock > 0:
			b.WriteString(body) // inside a string or block comment: verbatim
		case strings.TrimSpace(body) == "":
			// a blank line stays blank
		default:
			trimmed := strings.TrimLeft(body, " \t")
			col := 0
			if len(open) > 0 {
				col = open[len(open)-1]
				if trimmed[0] != ')' {
					col += 2
				}
			}
			b.WriteString(strings.Repeat(" ", col))
			b.WriteString(trimmed)
		}
		if li < len(lines)-1 {
			b.WriteByte('\n')
		}
		open, inString, inRaw, inBlock = scanLine(line, open, inString, inRaw, inBlock)
	}
	return b.String(), nil
}

// scanLine walks one line of src, updating the stack of open parenthesis
// columns and the string/comment state that carries across lines.
func scanLine(line string, open []int, inString, inRaw bool, inBlock int) ([]int, bool, bool, int) {
	i := 0
	for i < len(line) {
		if inBlock > 0 {
			switch {
			case strings.HasPrefix(line[i:], "#|"):
				inBlock++
				i += 2
			case strings.HasPrefix(line[i:], "|#"):
				inBlock--
				i += 2
			default:
				i++
			}
			continue
		}
		if inString {
			if line[i] == '\\' {
				i += 2
				continue
			}
			if line[i] == '"' {
				inString = false
			}
			i++
			continue
		}
		if inRaw {
			if strings.HasPrefix(line[i:], `"""`) {
				inRaw = false
				i += 3
				continue
			}
			i++
			continue
		}
		switch {
		case strings.HasPrefix(line[i:], "#|"):
			inBlock = 1
			i += 2
		case strings.HasPrefix(line[i:], `"""`):
			inRaw = true
			i += 3
		case line[i] == '"':
			inString = true
			i++
		case line[i] == ';':
			return open, inString, inRaw, inBlock // a line comment ends the line
		case line[i] == '#' && i+1 < len(line) && line[i+1] == '\\':
			// a character literal: #\x, #\space, #\( — no parenthesis inside
			i += 2
			if i < len(line) {
				if isLetter(line[i]) {
					for i < len(line) && isLetterDigit(line[i]) {
						i++
					}
				} else {
					i++
				}
			}
		case line[i] == '#' && i+1 < len(line) && line[i+1] == '(':
			open = append(open, i+1)
			i += 2
		case line[i] == '(':
			open = append(open, i)
			i++
		case line[i] == ')':
			if len(open) > 0 {
				open = open[:len(open)-1]
			}
			i++
		default:
			i++
		}
	}
	return open, inString, inRaw, inBlock
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}
func isLetterDigit(c byte) bool { return isLetter(c) || c >= '0' && c <= '9' }
