package builtins

import (
	"strings"
)

func expandEnvArgs(inv *Invocation, spec *CommandSpec, args []string) ([]string, error) {
	expanded := append([]string{}, args...)
	if _, ok := envAutoAction(inv, spec, expanded); ok {
		return expanded, nil
	}
	for {
		next, changed, err := expandOneEnvSplitArg(inv, expanded)
		if err != nil {
			return nil, err
		}
		if !changed {
			return expanded, nil
		}
		expanded = next
		if _, ok := envAutoAction(inv, spec, expanded); ok {
			return expanded, nil
		}
	}
}

func expandOneEnvSplitArg(inv *Invocation, args []string) ([]string, bool, error) {
	if args == nil {
		args = []string{}
	}
	parsingOptions := true
	out := make([]string, 0, len(args))
	for i := range len(args) {
		arg := args[i]
		if !parsingOptions {
			out = append(out, arg)
			continue
		}
		switch {
		case arg == "--":
			out = append(out, arg)
			parsingOptions = false
			continue
		case !strings.HasPrefix(arg, "-") || arg == "-":
			out = append(out, arg)
			parsingOptions = false
			continue
		case strings.HasPrefix(arg, "--"):
			value, consumed, ok := envLongSplitArg(args, i)
			if !ok {
				out = append(out, arg)
				continue
			}
			split, err := envSplitStringArgs(inv, value)
			if err != nil {
				return nil, false, err
			}
			out = append(out, split...)
			out = append(out, args[i+consumed+1:]...)
			return out, true, nil
		default:
			prefix, value, consumed, ok := envShortSplitArg(args, i)
			if !ok {
				out = append(out, arg)
				continue
			}
			split, err := envSplitStringArgs(inv, value)
			if err != nil {
				return nil, false, err
			}
			out = append(out, prefix...)
			out = append(out, split...)
			out = append(out, args[i+consumed+1:]...)
			return out, true, nil
		}
	}
	return out, false, nil
}

func envLongSplitArg(args []string, index int) (value string, consumed int, ok bool) {
	if args == nil {
		return "", 0, false
	}
	arg := args[index]
	switch {
	case arg == "--split-string":
		if index+1 >= len(args) {
			return "", 0, false
		}
		return args[index+1], 1, true
	case strings.HasPrefix(arg, "--split-string="):
		return strings.TrimPrefix(arg, "--split-string="), 0, true
	default:
		return "", 0, false
	}
}

func envShortSplitArg(args []string, index int) (prefix []string, value string, consumed int, ok bool) {
	if args == nil {
		return nil, "", 0, false
	}
	shorts := args[index][1:]
	for i := 0; i < len(shorts); i++ {
		switch shorts[i] {
		case '0', 'i', 'v':
			continue
		case 'S':
			for _, ch := range shorts[:i] {
				prefix = append(prefix, "-"+string(ch))
			}
			if i+1 < len(shorts) {
				return prefix, shorts[i+1:], 0, true
			}
			if index+1 >= len(args) {
				return nil, "", 0, false
			}
			return prefix, args[index+1], 1, true
		default:
			return nil, "", 0, false
		}
	}
	return nil, "", 0, false
}

func envSplitStringArgs(inv *Invocation, value string) ([]string, error) {
	lookup := func(name string) (string, bool) {
		if inv == nil || inv.Env == nil {
			return "", false
		}
		value, ok := inv.Env[name]
		return value, ok
	}
	return parseEnvSplitString(value, lookup, func(format string, args ...any) error {
		return exitf(inv, 125, "env: "+format, args...)
	})
}

func parseEnvSplitString(value string, lookup func(string) (string, bool), errf func(string, ...any) error) ([]string, error) {
	var (
		args        []string
		cur         strings.Builder
		haveCurrent bool
		sep         = true
		dq          bool
		sq          bool
	)

	startArg := func() {
		if !sep {
			return
		}
		if haveCurrent {
			args = append(args, cur.String())
			cur.Reset()
		}
		haveCurrent = true
		sep = false
	}

	appendByte := func(ch byte) {
		startArg()
		cur.WriteByte(ch)
	}

	for i := 0; i < len(value); i++ {
		ch := value[i]
		switch ch {
		case '\'':
			if dq {
				appendByte(ch)
				continue
			}
			startArg()
			sq = !sq
			continue
		case '"':
			if sq {
				appendByte(ch)
				continue
			}
			startArg()
			dq = !dq
			continue
		case ' ', '\t', '\n', '\v', '\f', '\r':
			if sq || dq {
				appendByte(ch)
				continue
			}
			sep = true
			for i+1 < len(value) && envSplitWhitespace(value[i+1]) {
				i++
			}
			continue
		case '#':
			if sep {
				i = len(value)
				break
			}
			appendByte(ch)
			continue
		case '\\':
			if sq && i+1 < len(value) && value[i+1] != '\\' && value[i+1] != '\'' {
				appendByte(ch)
				continue
			}
			i++
			if i >= len(value) {
				return nil, errf("invalid backslash at end of string in -S")
			}
			next := value[i]
			switch next {
			case '"', '#', '$', '\'', '\\':
				appendByte(next)
			case '_':
				if !dq {
					sep = true
					continue
				}
				appendByte(' ')
			case 'c':
				if dq {
					return nil, errf("'\\c' must not appear in double-quoted -S string")
				}
				i = len(value)
			case 'f':
				appendByte('\f')
			case 'n':
				appendByte('\n')
			case 'r':
				appendByte('\r')
			case 't':
				appendByte('\t')
			case 'v':
				appendByte('\v')
			default:
				return nil, errf("invalid sequence '\\%c' in -S", next)
			}
			continue
		case '$':
			if sq {
				appendByte(ch)
				continue
			}
			name, width := scanEnvSplitVarName(value[i:])
			if width == 0 {
				return nil, errf("only ${VARNAME} expansion is supported, error at: %s", value[i:])
			}
			if expanded, ok := lookup(name); ok && expanded != "" {
				startArg()
				cur.WriteString(expanded)
			}
			i += width - 1
			continue
		default:
			appendByte(ch)
		}
	}

	if dq || sq {
		return nil, errf("no terminating quote in -S string")
	}
	if haveCurrent {
		args = append(args, cur.String())
	}
	return args, nil
}

func scanEnvSplitVarName(text string) (name string, width int) {
	if len(text) < 4 || text[0] != '$' || text[1] != '{' {
		return "", 0
	}
	if !envSplitVarStart(text[2]) {
		return "", 0
	}
	i := 3
	for i < len(text) && envSplitVarContinue(text[i]) {
		i++
	}
	if i >= len(text) || text[i] != '}' {
		return "", 0
	}
	return text[2:i], i + 1
}

func envSplitWhitespace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\v' || ch == '\f' || ch == '\r'
}

func envSplitVarStart(ch byte) bool {
	return ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func envSplitVarContinue(ch byte) bool {
	return envSplitVarStart(ch) || (ch >= '0' && ch <= '9')
}
