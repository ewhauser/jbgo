package builtins

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type dateFormatToken struct {
	raw       string
	flags     string
	width     int
	hasWidth  bool
	modifier  rune
	directive string
}

func formatDateString(current time.Time, format string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(format); {
		if format[i] != '%' {
			b.WriteByte(format[i])
			i++
			continue
		}
		if i+1 >= len(format) {
			b.WriteByte('%')
			break
		}
		token, next, err := parseDateFormatToken(format, i)
		if err != nil {
			b.WriteString(format[i:])
			break
		}
		text, ok := renderDateFormatToken(current, token)
		if !ok {
			b.WriteString(token.raw)
		} else {
			b.WriteString(text)
		}
		i = next
	}
	return b.String(), nil
}

func parseDateFormatToken(format string, start int) (dateFormatToken, int, error) {
	i := start + 1
	for i < len(format) && strings.ContainsRune("-_0+^#", rune(format[i])) {
		i++
	}
	widthStart := i
	for i < len(format) && format[i] >= '0' && format[i] <= '9' {
		i++
	}
	var width int
	hasWidth := widthStart != i
	if hasWidth {
		value, err := strconv.Atoi(format[widthStart:i])
		if err != nil {
			return dateFormatToken{}, 0, err
		}
		width = value
	}
	var modifier rune
	if i < len(format) && (format[i] == 'E' || format[i] == 'O') {
		modifier = rune(format[i])
		i++
	}
	colonStart := i
	for i < len(format) && format[i] == ':' {
		i++
	}
	if i >= len(format) {
		return dateFormatToken{}, 0, fmt.Errorf("dangling %% in format")
	}
	directive := format[colonStart : i+1]
	return dateFormatToken{
		raw:       format[start : i+1],
		flags:     format[start+1 : widthStart],
		width:     width,
		hasWidth:  hasWidth,
		modifier:  modifier,
		directive: directive,
	}, i + 1, nil
}

func renderDateFormatToken(current time.Time, token dateFormatToken) (string, bool) {
	text, spec, ok := baseDateDirectiveValue(current, token.directive)
	if !ok {
		return "", false
	}
	return applyDateFormatModifiers(text, token.flags, token.width, spec, token.hasWidth), true
}

func baseDateDirectiveValue(current time.Time, directive string) (string, string, bool) {
	switch directive {
	case "%":
		return "%", "%", true
	case "a":
		return current.Format("Mon"), "a", true
	case "A":
		return current.Format("Monday"), "A", true
	case "b", "h":
		return current.Format("Jan"), "b", true
	case "B":
		return current.Format("January"), "B", true
	case "c":
		return current.Format("Mon Jan _2 15:04:05 2006"), "c", true
	case "C":
		return formatSignedNumber(current.Year()/100, 2, false), "C", true
	case "d":
		return fmt.Sprintf("%02d", current.Day()), "d", true
	case "D":
		return current.Format("01/02/06"), "D", true
	case "e":
		return fmt.Sprintf("%2d", current.Day()), "e", true
	case "F":
		return formatFullDate(current.Year(), current.Month(), current.Day()), "F", true
	case "g":
		year, _ := current.ISOWeek()
		return fmt.Sprintf("%02d", modPositive(year, 100)), "g", true
	case "G":
		year, _ := current.ISOWeek()
		return formatSignedNumber(year, 4, false), "G", true
	case "H":
		return fmt.Sprintf("%02d", current.Hour()), "H", true
	case "I":
		return formatHour12(current.Hour(), true), "I", true
	case "j":
		return fmt.Sprintf("%03d", current.YearDay()), "j", true
	case "k":
		return fmt.Sprintf("%2d", current.Hour()), "k", true
	case "l":
		return formatHour12(current.Hour(), false), "l", true
	case "m":
		return fmt.Sprintf("%02d", int(current.Month())), "m", true
	case "M":
		return fmt.Sprintf("%02d", current.Minute()), "M", true
	case "n":
		return "\n", "n", true
	case "N":
		return fmt.Sprintf("%09d", current.Nanosecond()), "N", true
	case "p":
		return current.Format("PM"), "p", true
	case "P":
		return strings.ToLower(current.Format("PM")), "P", true
	case "q":
		return strconv.Itoa((int(current.Month())-1)/3 + 1), "q", true
	case "r":
		return current.Format("03:04:05 PM"), "r", true
	case "R":
		return current.Format("15:04"), "R", true
	case "s":
		return strconv.FormatInt(current.Unix(), 10), "s", true
	case "S":
		return fmt.Sprintf("%02d", current.Second()), "S", true
	case "t":
		return "\t", "t", true
	case "T":
		return current.Format("15:04:05"), "T", true
	case "u":
		return strconv.Itoa(weekdayISO(current.Weekday())), "u", true
	case "U":
		return fmt.Sprintf("%02d", weekNumberSunday(current)), "U", true
	case "V":
		_, week := current.ISOWeek()
		return fmt.Sprintf("%02d", week), "V", true
	case "w":
		return strconv.Itoa(int(current.Weekday())), "w", true
	case "W":
		return fmt.Sprintf("%02d", weekNumberMonday(current)), "W", true
	case "x":
		return current.Format("01/02/06"), "x", true
	case "X":
		return current.Format("15:04:05"), "X", true
	case "y":
		return fmt.Sprintf("%02d", modPositive(current.Year(), 100)), "y", true
	case "Y":
		return formatSignedNumber(current.Year(), 4, false), "Y", true
	case "z":
		return formatTimezoneOffset(current, 0), "z", true
	case ":z":
		return formatTimezoneOffset(current, 1), "z", true
	case "::z":
		return formatTimezoneOffset(current, 2), "z", true
	case ":::z":
		return formatTimezoneOffset(current, 3), "z", true
	case "Z":
		return formatTimezoneName(current), "Z", true
	default:
		return "", "", false
	}
}

func applyDateFormatModifiers(value, flags string, width int, specifier string, explicitWidth bool) string {
	if specifier == "%" || specifier == "n" || specifier == "t" {
		return value
	}

	result := value
	defaultPad := '0'
	if isDateSpacePaddedSpecifier(specifier) {
		defaultPad = ' '
	}

	padChar := defaultPad
	noPad := false
	uppercase := false
	swapCase := false
	forceSign := false
	underscoreFlag := false

	for _, flag := range flags {
		switch flag {
		case '-':
			noPad = true
		case '_':
			noPad = false
			padChar = ' '
			underscoreFlag = true
		case '0':
			noPad = false
			padChar = '0'
		case '^':
			uppercase = true
			swapCase = false
		case '#':
			if !uppercase {
				swapCase = true
			}
		case '+':
			forceSign = true
			noPad = false
			padChar = '0'
		}
	}

	if uppercase {
		result = strings.ToUpper(result)
	} else if swapCase {
		if dateAllLettersUpper(result) {
			result = strings.ToLower(result)
		} else {
			result = strings.ToUpper(result)
		}
	}

	if noPad {
		return stripDateDefaultPadding(result)
	}

	effectiveWidth := width
	if !explicitWidth && (underscoreFlag || padChar != defaultPad) {
		effectiveWidth = dateDefaultWidth(specifier)
	}
	if effectiveWidth > 0 && effectiveWidth < len(result) {
		return stripDateDefaultPadding(result)
	}

	if !isDateTextSpecifier(specifier) && len(result) >= 2 {
		if padChar == ' ' && strings.HasPrefix(result, "0") {
			result = stripDateDefaultPadding(result)
		} else if padChar == '0' && strings.HasPrefix(result, " ") {
			result = stripDateDefaultPadding(result)
		}
	}

	if forceSign && !strings.HasPrefix(result, "+") && !strings.HasPrefix(result, "-") {
		if result != "" && result[0] >= '0' && result[0] <= '9' {
			defaultWidth := dateDefaultWidth(specifier)
			if explicitWidth || (defaultWidth > 0 && len(result) > defaultWidth) {
				result = "+" + result
			}
		}
	}

	if effectiveWidth > len(result) {
		padding := effectiveWidth - len(result)
		if padChar == '0' && (strings.HasPrefix(result, "+") || strings.HasPrefix(result, "-")) {
			sign := result[:1]
			result = sign + strings.Repeat("0", padding) + result[1:]
		} else {
			result = strings.Repeat(string(padChar), padding) + result
		}
	}
	return result
}

func isDateTextSpecifier(specifier string) bool {
	switch specifier {
	case "A", "a", "B", "b", "h", "Z", "p", "P", "c", "r", "R", "T", "D", "F", "X", "x":
		return true
	default:
		return false
	}
}

func isDateSpacePaddedSpecifier(specifier string) bool {
	switch specifier {
	case "A", "a", "B", "b", "h", "Z", "p", "P", "e", "k", "l", "c", "r", "R", "T", "D", "F", "X", "x":
		return true
	default:
		return false
	}
}

func dateDefaultWidth(specifier string) int {
	switch specifier {
	case "d", "e", "m", "H", "k", "I", "l", "M", "S", "y", "C", "U", "W", "V":
		return 2
	case "j", "N":
		if specifier == "N" {
			return 9
		}
		return 3
	case "w", "u", "q":
		return 1
	case "Y", "G":
		return 4
	case "g":
		return 2
	default:
		return 0
	}
}

func stripDateDefaultPadding(value string) string {
	if value == "" {
		return value
	}
	if value[0] == '0' && len(value) >= 2 {
		stripped := strings.TrimLeft(value, "0")
		if stripped == "" {
			return "0"
		}
		if stripped[0] >= '0' && stripped[0] <= '9' {
			return stripped
		}
	}
	if value[0] == ' ' {
		stripped := strings.TrimLeft(value, " ")
		if stripped != "" {
			return stripped
		}
	}
	return value
}

func dateAllLettersUpper(value string) bool {
	for _, r := range value {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			continue
		}
		if 'a' <= r && r <= 'z' {
			return false
		}
	}
	return true
}

func formatSignedNumber(value, width int, forcePlus bool) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	} else if forcePlus {
		sign = "+"
	}
	text := strconv.Itoa(value)
	if len(text) < width {
		text = strings.Repeat("0", width-len(text)) + text
	}
	return sign + text
}

func formatFullDate(year int, month time.Month, day int) string {
	yearText := formatSignedNumber(year, 4, year > 9999)
	return fmt.Sprintf("%s-%02d-%02d", yearText, int(month), day)
}

func formatTimezoneName(t time.Time) string {
	name, offset := t.Zone()
	if name != "" {
		return name
	}
	return formatOffsetValue(offset, 0)
}

func formatTimezoneOffset(t time.Time, precision int) string {
	_, offset := t.Zone()
	return formatOffsetValue(offset, precision)
}

func formatOffsetValue(offsetSeconds, precision int) string {
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	hours := offsetSeconds / 3600
	minutes := (offsetSeconds % 3600) / 60
	seconds := offsetSeconds % 60

	switch precision {
	case 0:
		return fmt.Sprintf("%s%02d%02d", sign, hours, minutes)
	case 1:
		return fmt.Sprintf("%s%02d:%02d", sign, hours, minutes)
	case 2:
		return fmt.Sprintf("%s%02d:%02d:%02d", sign, hours, minutes, seconds)
	case 3:
		if seconds != 0 {
			return fmt.Sprintf("%s%02d:%02d:%02d", sign, hours, minutes, seconds)
		}
		if minutes != 0 {
			return fmt.Sprintf("%s%02d:%02d", sign, hours, minutes)
		}
		return fmt.Sprintf("%s%02d", sign, hours)
	default:
		return fmt.Sprintf("%s%02d%02d", sign, hours, minutes)
	}
}

func weekNumberSunday(t time.Time) int {
	jan1 := time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, t.Location())
	offset := (7 - int(jan1.Weekday())) % 7
	yday := t.YearDay() - 1
	if yday < offset {
		return 0
	}
	return (yday-offset)/7 + 1
}

func weekNumberMonday(t time.Time) int {
	jan1 := time.Date(t.Year(), time.January, 1, 0, 0, 0, 0, t.Location())
	offset := (8 - weekdayISO(jan1.Weekday())) % 7
	yday := t.YearDay() - 1
	if yday < offset {
		return 0
	}
	return (yday-offset)/7 + 1
}

func modPositive(value, divisor int) int {
	result := value % divisor
	if result < 0 {
		result += divisor
	}
	return result
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
