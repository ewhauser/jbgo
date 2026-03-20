package printfutil

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

var (
	minusOneBig = big.NewInt(-1)
	minusTwoBig = big.NewInt(-2)
)

func formatTime(arg string, present bool, spec formatSpec, opts Options) (string, string) {
	when := opts.Now()
	if present {
		parsed := parseInteger(arg, true)
		if parsed.diagnose != "" {
			// Bash's %T doesn't have coverage for invalid operands in our corpus,
			// but staying consistent with numeric conversions is safer.
			sec := int64(0)
			if parsed.value != nil {
				sec = clampSigned64(parsed.value)
			}
			when = time.Unix(sec, 0)
			when = when.In(resolveLocation(opts))
			return applyStringFormat(strftime(spec.timeLayout, when), spec), parsed.diagnose
		}
		if parsed.value != nil {
			switch {
			case parsed.value.Cmp(minusOneBig) == 0:
				when = opts.Now()
			case parsed.value.Cmp(minusTwoBig) == 0:
				when = shellStartTime(opts)
			default:
				when = time.Unix(clampSigned64(parsed.value), 0)
			}
		}
	}
	when = when.In(resolveLocation(opts))
	text := strftime(spec.timeLayout, when)
	if len(text) >= 128 {
		text = ""
	}
	return applyStringFormat(text, spec), ""
}

func clampSigned64(value *big.Int) int64 {
	switch {
	case value == nil:
		return 0
	case value.Cmp(maxInt64Big) > 0:
		return math.MaxInt64
	case value.Cmp(minInt64Big) < 0:
		return math.MinInt64
	default:
		return value.Int64()
	}
}

func resolveLocation(opts Options) *time.Location {
	if opts.LookupEnv != nil {
		if value, ok := opts.LookupEnv("TZ"); ok && strings.TrimSpace(value) != "" {
			if loc, err := time.LoadLocation(value); err == nil {
				return loc
			}
		}
	}
	return time.Local
}

func shellStartTime(opts Options) time.Time {
	if !opts.StartTime.IsZero() {
		return opts.StartTime
	}
	return opts.Now()
}

func strftime(layout string, when time.Time) string {
	var b strings.Builder
	for i := 0; i < len(layout); i++ {
		if layout[i] != '%' || i+1 >= len(layout) {
			b.WriteByte(layout[i])
			continue
		}
		i++
		switch layout[i] {
		case '%':
			b.WriteByte('%')
		case 'a':
			b.WriteString(when.Format("Mon"))
		case 'A':
			b.WriteString(when.Format("Monday"))
		case 'b', 'h':
			b.WriteString(when.Format("Jan"))
		case 'B':
			b.WriteString(when.Format("January"))
		case 'C':
			fmt.Fprintf(&b, "%02d", when.Year()/100)
		case 'c':
			b.WriteString(when.Format("Mon Jan _2 15:04:05 2006"))
		case 'D':
			b.WriteString(when.Format("01/02/06"))
		case 'F':
			b.WriteString(when.Format("2006-01-02"))
		case 'Y':
			fmt.Fprintf(&b, "%04d", when.Year())
		case 'y':
			fmt.Fprintf(&b, "%02d", when.Year()%100)
		case 'm':
			fmt.Fprintf(&b, "%02d", int(when.Month()))
		case 'd':
			fmt.Fprintf(&b, "%02d", when.Day())
		case 'e':
			fmt.Fprintf(&b, "%2d", when.Day())
		case 'H':
			fmt.Fprintf(&b, "%02d", when.Hour())
		case 'I':
			b.WriteString(formatHour12(when.Hour(), true))
		case 'k':
			fmt.Fprintf(&b, "%2d", when.Hour())
		case 'l':
			b.WriteString(formatHour12(when.Hour(), false))
		case 'M':
			fmt.Fprintf(&b, "%02d", when.Minute())
		case 'p':
			b.WriteString(when.Format("PM"))
		case 'P':
			b.WriteString(strings.ToLower(when.Format("PM")))
		case 'R':
			b.WriteString(when.Format("15:04"))
		case 'S':
			fmt.Fprintf(&b, "%02d", when.Second())
		case 'T':
			b.WriteString(when.Format("15:04:05"))
		case 'j':
			fmt.Fprintf(&b, "%03d", when.YearDay())
		case 's':
			b.WriteString(strconv.FormatInt(when.Unix(), 10))
		case 'u':
			b.WriteString(strconv.Itoa(weekdayISO(when.Weekday())))
		case 'w':
			b.WriteString(strconv.Itoa(int(when.Weekday())))
		case 'x':
			b.WriteString(when.Format("01/02/06"))
		case 'X':
			b.WriteString(when.Format("15:04:05"))
		case 'z':
			b.WriteString(when.Format("-0700"))
		case 'Z':
			b.WriteString(when.Format("MST"))
		default:
			b.WriteByte('%')
			b.WriteByte(layout[i])
		}
	}
	return b.String()
}

func formatHour12(hour int, zeroPad bool) string {
	hour %= 12
	if hour == 0 {
		hour = 12
	}
	if zeroPad {
		return fmt.Sprintf("%02d", hour)
	}
	return fmt.Sprintf("%2d", hour)
}

func weekdayISO(day time.Weekday) int {
	if day == time.Sunday {
		return 7
	}
	return int(day)
}
