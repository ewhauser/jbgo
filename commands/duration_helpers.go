package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func parseFlexibleDuration(value string) (time.Duration, error) {
	if value == "" {
		return 0, fmt.Errorf("empty duration")
	}
	multiplier := time.Second
	last := value[len(value)-1]
	switch last {
	case 's':
		multiplier = time.Second
		value = value[:len(value)-1]
	case 'm':
		multiplier = time.Minute
		value = value[:len(value)-1]
	case 'h':
		multiplier = time.Hour
		value = value[:len(value)-1]
	case 'd':
		multiplier = 24 * time.Hour
		value = value[:len(value)-1]
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("invalid time interval %q", value)
	}
	return time.Duration(number * float64(multiplier)), nil
}

func decodeDelimiterValue(value string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			b.WriteByte(value[i])
			continue
		}
		if i+1 >= len(value) {
			b.WriteByte('\\')
			continue
		}
		switch value[i+1] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case '0':
			b.WriteByte(0)
		case '\\':
			b.WriteByte('\\')
		default:
			return "", fmt.Errorf("unsupported escape %q", value[i:i+2])
		}
		i++
	}
	return b.String(), nil
}
