package builtins

import (
	"math"
	"strconv"
	"strings"
)

func parseBlockSizeValue(inv *Invocation, commandName, value string) (int64, error) {
	rawValue := value
	value = strings.TrimSpace(value)
	for strings.HasPrefix(value, "'") {
		value = strings.TrimPrefix(value, "'")
	}
	switch strings.ToLower(value) {
	case "human-readable", "si":
		return 1, nil
	}
	if value == "" || value == "0" {
		return 0, exitf(inv, 1, "%s: invalid --block-size argument %s", commandName, quoteGNUOperand(rawValue))
	}
	multiplier := int64(1)
	lower := strings.ToLower(value)
	switch {
	case strings.HasSuffix(lower, "kib"):
		multiplier = 1024
		value = value[:len(value)-3]
	case strings.HasSuffix(lower, "mib"):
		multiplier = 1024 * 1024
		value = value[:len(value)-3]
	case strings.HasSuffix(lower, "gib"):
		multiplier = 1024 * 1024 * 1024
		value = value[:len(value)-3]
	case strings.HasSuffix(lower, "k"):
		multiplier = 1024
		value = value[:len(value)-1]
	case strings.HasSuffix(lower, "m"):
		multiplier = 1024 * 1024
		value = value[:len(value)-1]
	case strings.HasSuffix(lower, "g"):
		multiplier = 1024 * 1024 * 1024
		value = value[:len(value)-1]
	case strings.HasSuffix(lower, "b"):
		value = value[:len(value)-1]
	}
	if value == "" {
		value = "1"
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, exitf(inv, 1, "%s: invalid --block-size argument %s", commandName, quoteGNUOperand(rawValue))
	}
	total, ok := checkedNonNegativeInt64Product(n, multiplier)
	if !ok || total <= 0 {
		return 0, exitf(inv, 1, "%s: invalid --block-size argument %s", commandName, quoteGNUOperand(rawValue))
	}
	return total, nil
}

func checkedNonNegativeInt64Product(left, right int64) (int64, bool) {
	switch {
	case left < 0 || right < 0:
		return 0, false
	case left == 0 || right == 0:
		return 0, true
	case left > math.MaxInt64/right:
		return 0, false
	default:
		return left * right, true
	}
}
