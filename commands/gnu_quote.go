package commands

import "strings"

func quoteGNUOperand(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
