package shell

import "strings"

type pendingHeredoc struct {
	delimiter string
	stripTabs bool
}

type letNormalizer struct {
	src               string
	out               strings.Builder
	i                 int
	wantCommandWord   bool
	expectRedirTarget bool
	pendingHeredocOp  *pendingHeredoc
	pendingHeredocs   []pendingHeredoc
	caseDepth         int
	inCasePattern     bool
	sawCase           bool
	expectCaseIn      bool
}

func normalizeLetCommands(script string) string {
	if !strings.Contains(script, "let") {
		return script
	}

	n := letNormalizer{
		src:             script,
		wantCommandWord: true,
	}
	n.out.Grow(len(script))
	return n.run()
}

func (n *letNormalizer) run() string {
	for n.i < len(n.src) {
		switch c := n.src[n.i]; c {
		case ' ', '\t', '\r':
			n.out.WriteByte(c)
			n.i++
		case '\n':
			n.out.WriteByte(c)
			n.i++
			n.wantCommandWord = true
			n.expectRedirTarget = false
			n.pendingHeredocOp = nil
			n.consumeHeredocBodies()
		case '#':
			n.copyComment()
		case ';':
			if n.caseDepth > 0 && (strings.HasPrefix(n.src[n.i:], ";;") || strings.HasPrefix(n.src[n.i:], ";&")) {
				n.inCasePattern = true
			}
			n.copyOperator()
			n.wantCommandWord = true
			n.expectRedirTarget = false
		case '&':
			n.copyOperator()
			n.wantCommandWord = true
			n.expectRedirTarget = false
		case '|':
			n.copyOperator()
			n.wantCommandWord = true
			n.expectRedirTarget = false
		case '(':
			if strings.HasPrefix(n.src[n.i:], "((") {
				n.copyArithmeticCommand()
				continue
			}
			n.out.WriteByte(c)
			n.i++
			n.wantCommandWord = true
			n.expectRedirTarget = false
		case ')':
			n.out.WriteByte(c)
			n.i++
			if n.inCasePattern {
				n.inCasePattern = false
				n.wantCommandWord = true
			} else {
				n.wantCommandWord = false
			}
			n.expectRedirTarget = false
		case '{':
			n.out.WriteByte(c)
			n.i++
			n.wantCommandWord = true
			n.expectRedirTarget = false
		case '}':
			n.out.WriteByte(c)
			n.i++
			n.wantCommandWord = false
			n.expectRedirTarget = false
		case '!':
			n.out.WriteByte(c)
			n.i++
			n.wantCommandWord = true
			n.expectRedirTarget = false
		case '<', '>':
			n.copyRedirection()
		default:
			if isDigit(c) && n.peekRedirectionAfterDigits() {
				n.copyRedirection()
				continue
			}
			n.copyWord()
		}
	}
	return n.out.String()
}

func (n *letNormalizer) copyComment() {
	for n.i < len(n.src) && n.src[n.i] != '\n' {
		n.out.WriteByte(n.src[n.i])
		n.i++
	}
}

func (n *letNormalizer) copyOperator() {
	start := n.i
	switch {
	case strings.HasPrefix(n.src[n.i:], ";;&"):
		n.i += 3
	case strings.HasPrefix(n.src[n.i:], ";;"),
		strings.HasPrefix(n.src[n.i:], ";&"),
		strings.HasPrefix(n.src[n.i:], "&&"),
		strings.HasPrefix(n.src[n.i:], "||"),
		strings.HasPrefix(n.src[n.i:], "|&"):
		n.i += 2
	default:
		n.i++
	}
	n.out.WriteString(n.src[start:n.i])
}

func (n *letNormalizer) copyRedirection() {
	start := n.i
	for n.i < len(n.src) && isDigit(n.src[n.i]) {
		n.i++
	}

	stripTabs := false
	heredoc := false
	switch {
	case strings.HasPrefix(n.src[n.i:], "<<-"):
		n.i += 3
		stripTabs = true
		heredoc = true
	case strings.HasPrefix(n.src[n.i:], "<<<"):
		n.i += 3
	case strings.HasPrefix(n.src[n.i:], "<<"):
		n.i += 2
		heredoc = true
	case strings.HasPrefix(n.src[n.i:], "<&"),
		strings.HasPrefix(n.src[n.i:], ">&"),
		strings.HasPrefix(n.src[n.i:], "<>"),
		strings.HasPrefix(n.src[n.i:], ">>"),
		strings.HasPrefix(n.src[n.i:], ">|"):
		n.i += 2
	default:
		n.i++
	}

	n.out.WriteString(n.src[start:n.i])
	n.expectRedirTarget = true
	if heredoc {
		n.pendingHeredocOp = &pendingHeredoc{stripTabs: stripTabs}
	}
}

func (n *letNormalizer) copyWord() {
	start := n.i
	end := scanShellWord(n.src, start)
	raw := n.src[start:end]

	switch {
	case n.pendingHeredocOp != nil:
		n.out.WriteString(raw)
		hd := *n.pendingHeredocOp
		hd.delimiter = parseHeredocDelimiter(raw)
		n.pendingHeredocs = append(n.pendingHeredocs, hd)
		n.pendingHeredocOp = nil
		n.expectRedirTarget = false
	case n.expectRedirTarget:
		n.out.WriteString(raw)
		n.expectRedirTarget = false
	case n.sawCase:
		n.out.WriteString(raw)
		n.sawCase = false
		n.expectCaseIn = true
		n.wantCommandWord = false
	case n.expectCaseIn && raw == "in":
		n.out.WriteString(raw)
		n.expectCaseIn = false
		n.caseDepth++
		n.inCasePattern = true
		n.wantCommandWord = false
	case n.wantCommandWord && raw == "let" && !n.inCasePattern && !startsFunctionDeclaration(n.src, end):
		n.out.WriteString(letHelperCommandAlias)
		n.wantCommandWord = false
	default:
		n.out.WriteString(raw)
		if n.expectCaseIn {
			n.expectCaseIn = false
		}
		if n.wantCommandWord {
			switch {
			case raw == "case":
				n.sawCase = true
				n.wantCommandWord = false
			case raw == "esac" && n.caseDepth > 0:
				n.caseDepth--
				n.inCasePattern = false
				n.wantCommandWord = false
			case isAssignmentWord(raw) || keywordStartsCommand(raw):
				n.wantCommandWord = true
			default:
				n.wantCommandWord = false
			}
		}
	}

	n.i = end
}

func (n *letNormalizer) copyArithmeticCommand() {
	start := n.i
	end := scanArithmeticCommand(n.src, start)
	n.out.WriteString(n.src[start:end])
	n.i = end
	n.wantCommandWord = false
	n.expectRedirTarget = false
}

func (n *letNormalizer) consumeHeredocBodies() {
	for len(n.pendingHeredocs) > 0 && n.i < len(n.src) {
		lineStart := n.i
		lineEnd := strings.IndexByte(n.src[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(n.src)
		} else {
			lineEnd += lineStart
		}

		line := n.src[lineStart:lineEnd]
		n.out.WriteString(line)
		n.i = lineEnd
		if n.i < len(n.src) && n.src[n.i] == '\n' {
			n.out.WriteByte('\n')
			n.i++
		}

		compare := line
		if n.pendingHeredocs[0].stripTabs {
			compare = strings.TrimLeft(compare, "\t")
		}
		if compare == n.pendingHeredocs[0].delimiter {
			n.pendingHeredocs = n.pendingHeredocs[1:]
		}
	}
}

func peekRedirectionAfterDigits(src string, start int) bool {
	i := start
	for i < len(src) && isDigit(src[i]) {
		i++
	}
	return i < len(src) && (src[i] == '<' || src[i] == '>')
}

func (n *letNormalizer) peekRedirectionAfterDigits() bool {
	return peekRedirectionAfterDigits(n.src, n.i)
}

func keywordStartsCommand(word string) bool {
	switch word {
	case "if", "then", "do", "else", "elif", "while", "until":
		return true
	default:
		return false
	}
}

func startsFunctionDeclaration(src string, start int) bool {
	i := start
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\r':
			i++
		case '\\':
			if i+1 < len(src) && src[i+1] == '\n' {
				i += 2
			} else {
				return false
			}
		default:
			return src[i] == '('
		}
	}
	return false
}

func isAssignmentWord(word string) bool {
	if word == "" || !isNameStart(word[0]) {
		return false
	}
	i := 1
	for i < len(word) && isNameChar(word[i]) {
		i++
	}
	if i >= len(word) {
		return false
	}
	if word[i] == '=' {
		return true
	}
	return i+1 < len(word) && word[i] == '+' && word[i+1] == '='
}

func parseHeredocDelimiter(raw string) string {
	if len(raw) >= 2 {
		if raw[0] == '\'' && raw[len(raw)-1] == '\'' {
			return raw[1 : len(raw)-1]
		}
		if raw[0] == '"' && raw[len(raw)-1] == '"' {
			return raw[1 : len(raw)-1]
		}
	}

	var out strings.Builder
	out.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) {
			i++
		}
		out.WriteByte(raw[i])
	}
	return out.String()
}

func scanShellWord(src string, start int) int {
	i := start
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\r', '\n', ';', '&', '|', '<', '>', '(', ')', '{', '}':
			return i
		case '\'':
			i = skipSingleQuoted(src, i)
		case '"':
			i = skipDoubleQuoted(src, i)
		case '`':
			i = skipBackquoted(src, i)
		case '\\':
			if i+1 < len(src) {
				i += 2
			} else {
				i++
			}
		case '$':
			switch {
			case strings.HasPrefix(src[i:], "$(("):
				i = skipArithmeticExpansion(src, i)
			case strings.HasPrefix(src[i:], "$("):
				i = skipCommandSubstitution(src, i)
			case strings.HasPrefix(src[i:], "${"):
				i = skipBracedExpansion(src, i)
			default:
				i++
			}
		default:
			i++
		}
	}
	return i
}

func skipSingleQuoted(src string, start int) int {
	i := start + 1
	for i < len(src) {
		if src[i] == '\'' {
			return i + 1
		}
		i++
	}
	return len(src)
}

func skipDoubleQuoted(src string, start int) int {
	i := start + 1
	for i < len(src) {
		switch src[i] {
		case '\\':
			if i+1 < len(src) {
				i += 2
			} else {
				i++
			}
		case '"':
			return i + 1
		case '`':
			i = skipBackquoted(src, i)
		case '$':
			switch {
			case strings.HasPrefix(src[i:], "$(("):
				i = skipArithmeticExpansion(src, i)
			case strings.HasPrefix(src[i:], "$("):
				i = skipCommandSubstitution(src, i)
			case strings.HasPrefix(src[i:], "${"):
				i = skipBracedExpansion(src, i)
			default:
				i++
			}
		default:
			i++
		}
	}
	return len(src)
}

func skipBackquoted(src string, start int) int {
	i := start + 1
	for i < len(src) {
		switch src[i] {
		case '\\':
			if i+1 < len(src) {
				i += 2
			} else {
				i++
			}
		case '`':
			return i + 1
		default:
			i++
		}
	}
	return len(src)
}

func skipBracedExpansion(src string, start int) int {
	depth := 1
	i := start + 2
	for i < len(src) {
		switch src[i] {
		case '\\':
			if i+1 < len(src) {
				i += 2
			} else {
				i++
			}
		case '\'':
			i = skipSingleQuoted(src, i)
		case '"':
			i = skipDoubleQuoted(src, i)
		case '`':
			i = skipBackquoted(src, i)
		case '{':
			depth++
			i++
		case '}':
			depth--
			i++
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return len(src)
}

func skipArithmeticExpansion(src string, start int) int {
	depth := 1
	i := start + 3
	for i < len(src) {
		switch src[i] {
		case '\\':
			if i+1 < len(src) {
				i += 2
			} else {
				i++
			}
		case '\'':
			i = skipSingleQuoted(src, i)
		case '"':
			i = skipDoubleQuoted(src, i)
		case '`':
			i = skipBackquoted(src, i)
		case '(':
			depth++
			i++
		case ')':
			depth--
			i++
			if depth == 0 && i < len(src) && src[i] == ')' {
				return i + 1
			}
		default:
			i++
		}
	}
	return len(src)
}

func scanArithmeticCommand(src string, start int) int {
	depth := 1
	i := start + 2
	for i < len(src) {
		switch src[i] {
		case '\\':
			if i+1 < len(src) {
				i += 2
			} else {
				i++
			}
		case '\'':
			i = skipSingleQuoted(src, i)
		case '"':
			i = skipDoubleQuoted(src, i)
		case '`':
			i = skipBackquoted(src, i)
		case '$':
			switch {
			case strings.HasPrefix(src[i:], "$(("):
				i = skipArithmeticExpansion(src, i)
			case strings.HasPrefix(src[i:], "$("):
				i = skipCommandSubstitution(src, i)
			case strings.HasPrefix(src[i:], "${"):
				i = skipBracedExpansion(src, i)
			default:
				i++
			}
		case '(':
			depth++
			i++
		case ')':
			depth--
			i++
			if depth == 0 && i < len(src) && src[i] == ')' {
				return i + 1
			}
		default:
			i++
		}
	}
	return len(src)
}

func skipCommandSubstitution(src string, start int) int {
	depth := 1
	i := start + 2
	for i < len(src) {
		switch src[i] {
		case '\\':
			if i+1 < len(src) {
				i += 2
			} else {
				i++
			}
		case '\'':
			i = skipSingleQuoted(src, i)
		case '"':
			i = skipDoubleQuoted(src, i)
		case '`':
			i = skipBackquoted(src, i)
		case '$':
			switch {
			case strings.HasPrefix(src[i:], "$(("):
				i = skipArithmeticExpansion(src, i)
			case strings.HasPrefix(src[i:], "$("):
				depth++
				i += 2
			case strings.HasPrefix(src[i:], "${"):
				i = skipBracedExpansion(src, i)
			default:
				i++
			}
		case '(':
			depth++
			i++
		case ')':
			depth--
			i++
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return len(src)
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func isNameStart(ch byte) bool {
	return ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func isNameChar(ch byte) bool {
	return isNameStart(ch) || isDigit(ch)
}
