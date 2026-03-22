package builtins

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type dateParseInfo struct {
	Input        string
	DatePart     string
	TimePart     string
	ZonePart     string
	UsedMidnight bool
}

type dateRelativeDelta struct {
	years  int
	months int
	days   int
	span   time.Duration
}

type dateLayoutSpec struct {
	layout      string
	useLocation bool
	hasTime     bool
}

var dateKnownZoneOffsets = map[string]int{
	"UTC":  0,
	"GMT":  0,
	"UT":   0,
	"EST":  -5 * 3600,
	"EDT":  -4 * 3600,
	"CST":  -6 * 3600,
	"CDT":  -5 * 3600,
	"MST":  -7 * 3600,
	"MDT":  -6 * 3600,
	"PST":  -8 * 3600,
	"PDT":  -7 * 3600,
	"AKST": -9 * 3600,
	"AKDT": -8 * 3600,
	"HST":  -10 * 3600,
	"AWST": 8 * 3600,
	"ACST": 9*3600 + 30*60,
	"ACDT": 10*3600 + 30*60,
	"AEST": 10 * 3600,
	"AEDT": 11 * 3600,
	"NZST": 12 * 3600,
	"NZDT": 13 * 3600,
	"MEST": 2 * 3600,
	"EEST": 3 * 3600,
	"KST":  9 * 3600,
}

func parseDateValue(value string, base time.Time, loc *time.Location) (time.Time, dateParseInfo, error) {
	return parseDateValueInternal(value, base, loc, true)
}

func parseDateValueInternal(value string, base time.Time, loc *time.Location, allowRelativeSuffix bool) (time.Time, dateParseInfo, error) {
	if loc == nil {
		loc = time.Local
	}
	if base.IsZero() {
		base = time.Now().In(loc)
	} else {
		base = base.In(loc)
	}

	info := dateParseInfo{Input: value}

	if tzSpec, rest, ok := dateExtractTZPrefix(value); ok {
		prefixLoc, err := resolveDateLocationValue(tzSpec)
		if err != nil {
			return time.Time{}, info, err
		}
		parsed, inner, err := parseDateValueInternal(rest, base.In(prefixLoc), prefixLoc, allowRelativeSuffix)
		if err != nil {
			return time.Time{}, inner, err
		}
		inner.Input = value
		return parsed, inner, nil
	}

	cleaned := strings.TrimSpace(stripParenthesizedComments(value))
	info.Input = cleaned
	if cleaned == "" || strings.EqualFold(cleaned, "j") {
		current := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, base.Location())
		info = buildDateParseInfo(cleaned, current, true)
		return current, info, nil
	}

	if parsed, ok, err := parseDateEpochValue(cleaned); ok {
		if err != nil {
			return time.Time{}, info, err
		}
		info = buildDateParseInfo(cleaned, parsed, false)
		return parsed, info, nil
	}

	if parsed, meta, ok := parseDateAbsolute(cleaned, loc); ok {
		return parsed, meta, nil
	}

	if parsed, meta, ok := parseDatePureDigits(cleaned, base); ok {
		return parsed, meta, nil
	}

	if parsed, meta, ok := parseDateMilitaryToken(cleaned, base); ok {
		return parsed, meta, nil
	}

	if allowRelativeSuffix {
		if prefix, delta, ok := splitDateRelativeSuffix(cleaned); ok {
			anchor := base
			if prefix != "" {
				parsedPrefix, _, err := parseDateValueInternal(prefix, base, loc, false)
				if err != nil {
					return time.Time{}, info, err
				}
				anchor = parsedPrefix
			}
			parsed := applyDateRelative(anchor, delta)
			info = buildDateParseInfo(cleaned, parsed, false)
			return parsed, info, nil
		}
	}

	if parsed, meta, ok := parseDateRelativeExpression(cleaned, base, loc); ok {
		return parsed, meta, nil
	}

	return time.Time{}, info, fmt.Errorf("unsupported date")
}

func buildDateParseInfo(input string, parsed time.Time, usedMidnight bool) dateParseInfo {
	return dateParseInfo{
		Input:        input,
		DatePart:     parsed.Format("2006-01-02"),
		TimePart:     parsed.Format("15:04:05"),
		ZonePart:     formatTimezoneName(parsed),
		UsedMidnight: usedMidnight,
	}
}

func stripParenthesizedComments(input string) string {
	if !strings.Contains(input, "(") {
		return input
	}
	var b strings.Builder
	depth := 0
	for _, r := range input {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func dateExtractTZPrefix(value string) (string, string, bool) {
	if !strings.HasPrefix(value, "TZ=") {
		return "", "", false
	}
	rest := value[3:]
	if rest == "" {
		return "", "", false
	}
	switch rest[0] {
	case '"', '\'':
		quote := rest[0]
		end := strings.IndexByte(rest[1:], quote)
		if end < 0 {
			return "", "", false
		}
		spec := rest[1 : end+1]
		remainder := strings.TrimSpace(rest[end+2:])
		return spec, remainder, true
	default:
		fields := strings.Fields(rest)
		if len(fields) < 2 {
			return "", "", false
		}
		spec := fields[0]
		remainder := strings.TrimSpace(strings.TrimPrefix(rest, spec))
		return spec, remainder, true
	}
}

func resolveDateLocationFromEnv(env map[string]string, utc bool) *time.Location {
	if utc {
		return time.UTC
	}
	if env != nil {
		if value, ok := env["TZ"]; ok {
			if loc, err := resolveDateLocationValue(value); err == nil {
				return loc
			}
		}
	}
	return time.Local
}

func resolveDateLocationValue(value string) (*time.Location, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.UTC, nil
	}
	switch strings.ToUpper(value) {
	case "UTC", "UTC0", "GMT", "GMT0", "UT":
		return time.UTC, nil
	}
	if loc, err := time.LoadLocation(value); err == nil {
		return loc, nil
	}
	if loc, ok := parseDatePOSIXZone(value); ok {
		return loc, nil
	}
	if offset, ok := parseDateNumericOffset(value); ok {
		return time.FixedZone(value, offset), nil
	}
	return nil, fmt.Errorf("invalid timezone")
}

func parseDatePOSIXZone(value string) (*time.Location, bool) {
	if len(value) < 4 {
		return nil, false
	}
	nameEnd := 0
	for nameEnd < len(value) && ((value[nameEnd] >= 'A' && value[nameEnd] <= 'Z') || (value[nameEnd] >= 'a' && value[nameEnd] <= 'z')) {
		nameEnd++
	}
	if nameEnd < 3 || nameEnd == len(value) {
		return nil, false
	}
	hours, minutes, ok := parseDateHourMinuteOffset(value[nameEnd:])
	if !ok {
		return nil, false
	}
	offset := -(hours*3600 + minutes*60)
	return time.FixedZone(value[:nameEnd], offset), true
}

func parseDateEpochValue(value string) (time.Time, bool, error) {
	if !strings.HasPrefix(value, "@") {
		return time.Time{}, false, nil
	}
	raw := strings.TrimSpace(value[1:])
	if raw == "" {
		return time.Time{}, true, fmt.Errorf("invalid epoch")
	}
	neg := false
	if raw[0] == '+' || raw[0] == '-' {
		neg = raw[0] == '-'
		raw = raw[1:]
	}
	whole, frac, _ := strings.Cut(raw, ".")
	if whole == "" {
		whole = "0"
	}
	seconds, err := strconv.ParseInt(signPrefix(whole, neg), 10, 64)
	if err != nil {
		return time.Time{}, true, err
	}
	nanos := 0
	if frac != "" {
		if len(frac) > 9 {
			frac = frac[:9]
		}
		frac += strings.Repeat("0", 9-len(frac))
		value, err := strconv.Atoi(frac)
		if err != nil {
			return time.Time{}, true, err
		}
		nanos = value
		if neg {
			nanos = -nanos
		}
	}
	return time.Unix(seconds, int64(nanos)).UTC(), true, nil
}

func signPrefix(value string, neg bool) string {
	if neg {
		return "-" + value
	}
	return value
}

func parseDateAbsolute(value string, loc *time.Location) (time.Time, dateParseInfo, bool) {
	if parsed, info, ok := parseDateAbsoluteWithLayouts(value, loc, dateAbsoluteLayouts()); ok {
		return parsed, info, true
	}
	if rewritten, zone, ok := rewriteKnownTimezoneSuffix(value); ok {
		if parsed, info, ok := parseDateAbsoluteWithLayouts(rewritten, time.UTC, dateOffsetLayouts()); ok {
			info.Input = value
			info.ZonePart = zone
			return parsed, info, true
		}
	}
	if parsed, info, ok := parseDateAbsoluteWithLayouts(value, time.UTC, dateOffsetLayouts()); ok {
		return parsed, info, true
	}
	return time.Time{}, dateParseInfo{}, false
}

func parseDateAbsoluteWithLayouts(value string, loc *time.Location, layouts []dateLayoutSpec) (time.Time, dateParseInfo, bool) {
	for _, spec := range layouts {
		var (
			parsed time.Time
			err    error
		)
		if spec.useLocation {
			parsed, err = time.ParseInLocation(spec.layout, value, loc)
		} else {
			parsed, err = time.Parse(spec.layout, value)
		}
		if err != nil {
			continue
		}
		info := buildDateParseInfo(value, parsed, !spec.hasTime)
		return parsed, info, true
	}
	return time.Time{}, dateParseInfo{}, false
}

func dateAbsoluteLayouts() []dateLayoutSpec {
	return []dateLayoutSpec{
		{layout: time.RFC3339Nano, hasTime: true},
		{layout: time.RFC3339, hasTime: true},
		{layout: "2006-01-02T15:04:05.999999999", useLocation: true, hasTime: true},
		{layout: "2006-01-02T15:04:05", useLocation: true, hasTime: true},
		{layout: "2006-01-02T15:04", useLocation: true, hasTime: true},
		{layout: "2006-01-02 15:04:05.999999999", useLocation: true, hasTime: true},
		{layout: "2006-01-02 15:04:05", useLocation: true, hasTime: true},
		{layout: "2006-01-02 15:04", useLocation: true, hasTime: true},
		{layout: "2006-1-2 15:04:05", useLocation: true, hasTime: true},
		{layout: "2006-1-2 15:04", useLocation: true, hasTime: true},
		{layout: "2006/01/02 15:04:05", useLocation: true, hasTime: true},
		{layout: "2006/01/02 15:04", useLocation: true, hasTime: true},
		{layout: "2006/1/2 15:04:05", useLocation: true, hasTime: true},
		{layout: "2006/1/2 15:04", useLocation: true, hasTime: true},
		{layout: "Mon, 02 Jan 2006 15:04:05", useLocation: true, hasTime: true},
		{layout: "Mon 02 Jan 2006 15:04:05", useLocation: true, hasTime: true},
		{layout: "02 Jan 2006 15:04:05", useLocation: true, hasTime: true},
		{layout: "Mon Jan 2 15:04:05 2006", useLocation: true, hasTime: true},
		{layout: "Jan 2 15:04:05 2006", useLocation: true, hasTime: true},
		{layout: "2006-01-02", useLocation: true},
		{layout: "2006-1-2", useLocation: true},
		{layout: "2006/01/02", useLocation: true},
		{layout: "2006/1/2", useLocation: true},
	}
}

func dateOffsetLayouts() []dateLayoutSpec {
	return []dateLayoutSpec{
		{layout: time.RFC3339Nano, hasTime: true},
		{layout: time.RFC3339, hasTime: true},
		{layout: "2006-01-02 15:04:05.999999999 -07:00", hasTime: true},
		{layout: "2006-01-02 15:04:05 -07:00", hasTime: true},
		{layout: "2006-01-02 15:04 -07:00", hasTime: true},
		{layout: "2006-01-02 15:04:05-07:00", hasTime: true},
		{layout: "2006-01-02 15:04-07:00", hasTime: true},
		{layout: "2006-01-02 15:04:05 -0700", hasTime: true},
		{layout: "2006-01-02 15:04 -0700", hasTime: true},
		{layout: "2006-01-02 15:04:05-0700", hasTime: true},
		{layout: "2006-01-02 15:04-0700", hasTime: true},
		{layout: "Mon, 02 Jan 2006 15:04:05 -0700", hasTime: true},
		{layout: "Mon, 02 Jan 2006 15:04:05 -07:00", hasTime: true},
		{layout: "Mon, 02 Jan 2006 15:04:05 MST", hasTime: true},
		{layout: "Mon 02 Jan 2006 15:04:05 MST", hasTime: true},
		{layout: "02 Jan 2006 15:04:05 MST", hasTime: true},
		{layout: "2006-01-02 15:04:05 MST", hasTime: true},
		{layout: "2006-01-02 15:04 MST", hasTime: true},
	}
}

func rewriteKnownTimezoneSuffix(value string) (string, string, bool) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "", "", false
	}
	last := fields[len(fields)-1]
	offset, ok := dateKnownZoneOffsets[strings.ToUpper(last)]
	if !ok {
		return "", "", false
	}
	fields[len(fields)-1] = formatOffsetValue(offset, 0)
	return strings.Join(fields, " "), last, true
}

func parseDatePureDigits(value string, base time.Time) (time.Time, dateParseInfo, bool) {
	if value == "" || len(value) > 4 {
		return time.Time{}, dateParseInfo{}, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return time.Time{}, dateParseInfo{}, false
		}
	}
	var hour, minute int
	if len(value) <= 2 {
		hour, _ = strconv.Atoi(value)
	} else {
		hour, _ = strconv.Atoi(value[:len(value)-2])
		minute, _ = strconv.Atoi(value[len(value)-2:])
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return time.Time{}, dateParseInfo{}, false
	}
	parsed := time.Date(base.Year(), base.Month(), base.Day(), hour, minute, 0, 0, base.Location())
	return parsed, buildDateParseInfo(value, parsed, false), true
}

func parseDateMilitaryToken(value string, base time.Time) (time.Time, dateParseInfo, bool) {
	if !isDateMilitaryToken(value) {
		return time.Time{}, dateParseInfo{}, false
	}
	if strings.EqualFold(value, "j") {
		parsed := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, base.Location())
		return parsed, buildDateParseInfo(value, parsed, true), true
	}
	hour, dayDelta, ok := parseDateMilitaryHour(value)
	if !ok {
		return time.Time{}, dateParseInfo{}, false
	}
	datePart := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, time.UTC)
	datePart = datePart.AddDate(0, 0, dayDelta)
	parsed := time.Date(datePart.Year(), datePart.Month(), datePart.Day(), hour, 0, 0, 0, time.UTC)
	return parsed, buildDateParseInfo(value, parsed, false), true
}

func isDateMilitaryToken(value string) bool {
	if value == "" || len(value) > 3 {
		return false
	}
	first := value[0]
	if (first < 'A' || first > 'Z') && (first < 'a' || first > 'z') {
		return false
	}
	for i := 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func parseDateMilitaryHour(value string) (int, int, bool) {
	letter := value[0]
	if letter >= 'A' && letter <= 'Z' {
		letter = letter - 'A' + 'a'
	}
	if letter == 'j' {
		return 0, 0, true
	}
	var extra int
	if len(value) > 1 {
		number, err := strconv.Atoi(value[1:])
		if err != nil {
			return 0, 0, false
		}
		extra = number
	}
	var tzOffset int
	switch {
	case letter >= 'a' && letter <= 'i':
		tzOffset = int(letter-'a') + 1
	case letter >= 'k' && letter <= 'm':
		tzOffset = int(letter-'k') + 10
	case letter >= 'n' && letter <= 'y':
		tzOffset = -(int(letter-'n') + 1)
	case letter == 'z':
		tzOffset = 0
	default:
		return 0, 0, false
	}
	raw := extra - tzOffset
	dayDelta := 0
	switch {
	case raw < 0:
		dayDelta = -1
	case raw >= 24:
		dayDelta = 1
	}
	hour := raw % 24
	if hour < 0 {
		hour += 24
	}
	return hour, dayDelta, true
}

func splitDateRelativeSuffix(value string) (string, dateRelativeDelta, bool) {
	fields := strings.Fields(value)
	for tailLen := 4; tailLen >= 2; tailLen-- {
		if len(fields) < tailLen {
			continue
		}
		delta, ok := parseDateRelativeFields(fields[len(fields)-tailLen:])
		if !ok {
			continue
		}
		prefix := strings.Join(fields[:len(fields)-tailLen], " ")
		return strings.TrimSpace(prefix), delta, true
	}
	return "", dateRelativeDelta{}, false
}

func parseDateRelativeExpression(value string, base time.Time, loc *time.Location) (time.Time, dateParseInfo, bool) {
	if delta, ok := parseDateRelativeFields(strings.Fields(value)); ok {
		parsed := applyDateRelative(base, delta)
		return parsed, buildDateParseInfo(value, parsed, false), true
	}
	if parsed, info, ok := parseDateKeywordExpression(value, base, loc); ok {
		return parsed, info, true
	}
	return time.Time{}, dateParseInfo{}, false
}

func parseDateRelativeFields(fields []string) (dateRelativeDelta, bool) {
	if len(fields) < 2 || len(fields) > 4 {
		return dateRelativeDelta{}, false
	}
	sign := 1
	idx := 0
	if fields[idx] == "+" || fields[idx] == "-" {
		if fields[idx] == "-" {
			sign = -1
		}
		idx++
		if len(fields)-idx < 2 {
			return dateRelativeDelta{}, false
		}
	}
	amount, err := strconv.Atoi(fields[idx])
	if err != nil {
		return dateRelativeDelta{}, false
	}
	idx++
	unit := strings.ToLower(fields[idx])
	idx++
	if idx < len(fields) {
		if !strings.EqualFold(fields[idx], "ago") {
			return dateRelativeDelta{}, false
		}
		sign *= -1
	}
	amount *= sign
	switch unit {
	case "second", "seconds":
		return dateRelativeDelta{span: time.Duration(amount) * time.Second}, true
	case "minute", "minutes":
		return dateRelativeDelta{span: time.Duration(amount) * time.Minute}, true
	case "hour", "hours":
		return dateRelativeDelta{span: time.Duration(amount) * time.Hour}, true
	case "day", "days":
		return dateRelativeDelta{days: amount}, true
	case "week", "weeks":
		return dateRelativeDelta{days: amount * 7}, true
	case "month", "months":
		return dateRelativeDelta{months: amount}, true
	case "year", "years":
		return dateRelativeDelta{years: amount}, true
	default:
		return dateRelativeDelta{}, false
	}
}

func applyDateRelative(base time.Time, delta dateRelativeDelta) time.Time {
	shifted := base.AddDate(delta.years, delta.months, delta.days)
	if delta.span != 0 {
		shifted = shifted.Add(delta.span)
	}
	return shifted
}

func parseDateKeywordExpression(value string, base time.Time, loc *time.Location) (time.Time, dateParseInfo, bool) {
	tokens := strings.Fields(value)
	if len(tokens) == 0 {
		return time.Time{}, dateParseInfo{}, false
	}

	timeToken := ""
	zoneLoc := loc

	if parsedLoc, ok := parseDateZoneToken(tokens[len(tokens)-1]); ok && len(tokens) > 1 {
		zoneLoc = parsedLoc
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) > 1 {
		if _, _, _, ok := parseDateClockToken(tokens[0]); ok {
			timeToken = tokens[0]
			tokens = tokens[1:]
		} else if _, _, _, ok := parseDateClockToken(tokens[len(tokens)-1]); ok {
			timeToken = tokens[len(tokens)-1]
			tokens = tokens[:len(tokens)-1]
		}
	}

	current := base.In(zoneLoc)
	anchor, ok := parseDateKeywordFields(tokens, current)
	if !ok {
		return time.Time{}, dateParseInfo{}, false
	}
	if timeToken != "" {
		hour, minute, second, ok := parseDateClockToken(timeToken)
		if !ok {
			return time.Time{}, dateParseInfo{}, false
		}
		anchor = time.Date(anchor.Year(), anchor.Month(), anchor.Day(), hour, minute, second, 0, zoneLoc)
	} else {
		anchor = time.Date(anchor.Year(), anchor.Month(), anchor.Day(), current.Hour(), current.Minute(), current.Second(), current.Nanosecond(), zoneLoc)
	}
	return anchor, buildDateParseInfo(value, anchor, timeToken == "" && (len(tokens) == 1 && (strings.EqualFold(tokens[0], "today") || strings.EqualFold(tokens[0], "yesterday") || strings.EqualFold(tokens[0], "tomorrow")))), true
}

func parseDateKeywordFields(tokens []string, current time.Time) (time.Time, bool) {
	switch len(tokens) {
	case 1:
		switch strings.ToLower(tokens[0]) {
		case "today":
			return current, true
		case "yesterday":
			return current.AddDate(0, 0, -1), true
		case "tomorrow":
			return current.AddDate(0, 0, 1), true
		}
	case 2:
		qualifier := strings.ToLower(tokens[0])
		target := strings.ToLower(tokens[1])
		switch target {
		case "day":
			switch qualifier {
			case "last":
				return current.AddDate(0, 0, -1), true
			case "this":
				return current, true
			case "next":
				return current.AddDate(0, 0, 1), true
			}
		case "week":
			switch qualifier {
			case "last":
				return current.AddDate(0, 0, -7), true
			case "this":
				return current, true
			case "next":
				return current.AddDate(0, 0, 7), true
			}
		case "month":
			switch qualifier {
			case "last":
				return current.AddDate(0, -1, 0), true
			case "this":
				return current, true
			case "next":
				return current.AddDate(0, 1, 0), true
			}
		case "year":
			switch qualifier {
			case "last":
				return current.AddDate(-1, 0, 0), true
			case "this":
				return current, true
			case "next":
				return current.AddDate(1, 0, 0), true
			}
		default:
			weekday, ok := parseDateWeekday(target)
			if !ok {
				return time.Time{}, false
			}
			switch qualifier {
			case "last":
				return current.AddDate(0, 0, -daysSinceWeekday(current.Weekday(), weekday)), true
			case "this":
				return current.AddDate(0, 0, daysToWeekdayInISOWeek(current.Weekday(), weekday)), true
			case "next":
				return current.AddDate(0, 0, daysUntilWeekday(current.Weekday(), weekday)), true
			}
		}
	}
	return time.Time{}, false
}

func parseDateWeekday(value string) (time.Weekday, bool) {
	switch strings.ToLower(value) {
	case "sun", "sunday":
		return time.Sunday, true
	case "mon", "monday":
		return time.Monday, true
	case "tue", "tues", "tuesday":
		return time.Tuesday, true
	case "wed", "wednesday":
		return time.Wednesday, true
	case "thu", "thurs", "thursday":
		return time.Thursday, true
	case "fri", "friday":
		return time.Friday, true
	case "sat", "saturday":
		return time.Saturday, true
	default:
		return time.Sunday, false
	}
}

func daysUntilWeekday(current, target time.Weekday) int {
	delta := (int(target) - int(current) + 7) % 7
	if delta == 0 {
		return 7
	}
	return delta
}

func daysSinceWeekday(current, target time.Weekday) int {
	delta := (int(current) - int(target) + 7) % 7
	if delta == 0 {
		return 7
	}
	return delta
}

func daysToWeekdayInISOWeek(current, target time.Weekday) int {
	return weekdayISO(target) - weekdayISO(current)
}

func parseDateClockToken(value string) (int, int, int, bool) {
	if strings.Contains(value, ":") {
		parts := strings.Split(value, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return 0, 0, 0, false
		}
		hour, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, 0, false
		}
		minute, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, 0, false
		}
		second := 0
		if len(parts) == 3 {
			second, err = strconv.Atoi(parts[2])
			if err != nil {
				return 0, 0, 0, false
			}
		}
		if hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 60 {
			return 0, 0, 0, false
		}
		return hour, minute, second, true
	}
	if value == "" || len(value) > 4 {
		return 0, 0, 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, 0, 0, false
		}
	}
	if len(value) <= 2 {
		hour, _ := strconv.Atoi(value)
		if hour < 0 || hour > 23 {
			return 0, 0, 0, false
		}
		return hour, 0, 0, true
	}
	hour, _ := strconv.Atoi(value[:len(value)-2])
	minute, _ := strconv.Atoi(value[len(value)-2:])
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, 0, false
	}
	return hour, minute, 0, true
}

func parseDateZoneToken(value string) (*time.Location, bool) {
	if loc, err := resolveDateLocationValue(value); err == nil {
		return loc, true
	}
	if offset, ok := dateKnownZoneOffsets[strings.ToUpper(value)]; ok {
		return time.FixedZone(value, offset), true
	}
	if offset, ok := parseDateNumericOffset(value); ok {
		return time.FixedZone(value, offset), true
	}
	return nil, false
}

func parseDateNumericOffset(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	sign := 1
	switch value[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return 0, false
	}
	raw := value[1:]
	hours, minutes, ok := parseDateHourMinuteOffset(raw)
	if !ok {
		return 0, false
	}
	return sign * (hours*3600 + minutes*60), true
}

func parseDateHourMinuteOffset(raw string) (int, int, bool) {
	switch {
	case strings.Contains(raw, ":"):
		parts := strings.Split(raw, ":")
		if len(parts) < 1 || len(parts) > 2 {
			return 0, 0, false
		}
		hours, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, false
		}
		minutes := 0
		if len(parts) == 2 {
			minutes, err = strconv.Atoi(parts[1])
			if err != nil {
				return 0, 0, false
			}
		}
		if hours < 0 || hours > 23 || minutes < 0 || minutes > 59 {
			return 0, 0, false
		}
		return hours, minutes, true
	case len(raw) == 1 || len(raw) == 2:
		hours, err := strconv.Atoi(raw)
		if err != nil || hours < 0 || hours > 23 {
			return 0, 0, false
		}
		return hours, 0, true
	case len(raw) == 4:
		hours, err := strconv.Atoi(raw[:2])
		if err != nil {
			return 0, 0, false
		}
		minutes, err := strconv.Atoi(raw[2:])
		if err != nil {
			return 0, 0, false
		}
		if hours < 0 || hours > 23 || minutes < 0 || minutes > 59 {
			return 0, 0, false
		}
		return hours, minutes, true
	default:
		return 0, 0, false
	}
}

func isDateLegacyTimestamp(value string) bool {
	main := value
	frac := ""
	if head, tail, ok := strings.Cut(value, "."); ok {
		main = head
		frac = tail
	}
	if len(main) != 8 && len(main) != 10 && len(main) != 12 {
		return false
	}
	for _, r := range main {
		if r < '0' || r > '9' {
			return false
		}
	}
	if frac != "" {
		if len(frac) != 2 {
			return false
		}
		for _, r := range frac {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func parseDateLegacyTimestamp(value string, now time.Time, loc *time.Location) (time.Time, error) {
	main := value
	second := 0
	if head, tail, ok := strings.Cut(value, "."); ok {
		main = head
		number, err := strconv.Atoi(tail)
		if err != nil {
			return time.Time{}, err
		}
		second = number
	}
	var (
		month, day, hour, minute, year int
		err                            error
	)
	month, err = strconv.Atoi(main[0:2])
	if err != nil {
		return time.Time{}, err
	}
	day, err = strconv.Atoi(main[2:4])
	if err != nil {
		return time.Time{}, err
	}
	hour, err = strconv.Atoi(main[4:6])
	if err != nil {
		return time.Time{}, err
	}
	minute, err = strconv.Atoi(main[6:8])
	if err != nil {
		return time.Time{}, err
	}
	switch len(main) {
	case 8:
		year = now.In(loc).Year()
	case 10:
		shortYear, err := strconv.Atoi(main[8:10])
		if err != nil {
			return time.Time{}, err
		}
		if shortYear >= 69 {
			year = 1900 + shortYear
		} else {
			year = 2000 + shortYear
		}
	case 12:
		year, err = strconv.Atoi(main[8:12])
		if err != nil {
			return time.Time{}, err
		}
	default:
		return time.Time{}, fmt.Errorf("invalid legacy timestamp")
	}
	parsed := time.Date(year, time.Month(month), day, hour, minute, second, 0, loc)
	if parsed.Month() != time.Month(month) || parsed.Day() != day || parsed.Hour() != hour || parsed.Minute() != minute || parsed.Second() != second {
		return time.Time{}, fmt.Errorf("invalid legacy timestamp")
	}
	return parsed, nil
}
