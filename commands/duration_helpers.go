package commands

import (
	"fmt"
	"strconv"
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
