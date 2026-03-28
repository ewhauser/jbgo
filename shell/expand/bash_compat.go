package expand

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	"github.com/ewhauser/gbash/shell/syntax"
)

type InvalidIndirectExpansionError struct {
	Ref string
}

func (e InvalidIndirectExpansionError) Error() string {
	if e.Ref == "" {
		return "invalid indirect expansion"
	}
	return fmt.Sprintf("%s: invalid indirect expansion", e.Ref)
}

type InvalidVariableNameError struct {
	Ref string
}

func (e InvalidVariableNameError) Error() string {
	if e.Ref == "" {
		return "invalid variable name"
	}
	return fmt.Sprintf("%s: invalid variable name", e.Ref)
}

func (cfg *Config) bashByteLocale() bool {
	locale := cfg.envGet("LC_ALL")
	if locale == "" {
		locale = cfg.envGet("LC_CTYPE")
	}
	if locale == "" {
		locale = cfg.envGet("LANG")
	}
	switch locale {
	case "C", "POSIX":
		return true
	default:
		return false
	}
}

func (cfg *Config) bashStringLen(str string) int {
	if cfg.bashByteLocale() {
		return len(str)
	}
	return utf8.RuneCountInString(str)
}

func (cfg *Config) bashStringSlice(str string, offsetSet bool, offset int, lengthSet bool, length int) string {
	if cfg.bashByteLocale() || !utf8.ValidString(str) {
		slicePos := func(n int) int {
			if n < 0 {
				n = len(str) + n
				if n < 0 {
					n = len(str)
				}
			} else if n > len(str) {
				n = len(str)
			}
			return n
		}
		if offsetSet {
			str = str[slicePos(offset):]
		}
		if lengthSet {
			str = str[:slicePos(length)]
		}
		return str
	}

	runes := []rune(str)
	slicePos := func(n int) int {
		if n < 0 {
			n = len(runes) + n
			if n < 0 {
				n = len(runes)
			}
		} else if n > len(runes) {
			n = len(runes)
		}
		return n
	}
	if offsetSet {
		runes = runes[slicePos(offset):]
	}
	if lengthSet {
		runes = runes[:slicePos(length)]
	}
	return string(runes)
}

func varRefString(ref *syntax.VarRef) string {
	if ref == nil {
		return ""
	}
	var buf bytes.Buffer
	printer := syntax.NewPrinter()
	if err := printer.Print(&buf, ref); err != nil {
		return ref.Name.Value
	}
	return buf.String()
}

func paramExpOperandString(pe *syntax.ParamExp) string {
	if pe == nil || pe.Param == nil {
		return ""
	}
	ref := varRefString(&syntax.VarRef{
		Name:  pe.Param,
		Index: pe.Index,
	})
	if pe.Excl {
		return "!" + ref
	}
	return ref
}
