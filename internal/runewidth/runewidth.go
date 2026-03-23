// Package runewidth provides character display width calculation.
//
// This is a minimal vendored subset of github.com/mattn/go-runewidth
// (MIT License, Copyright (c) 2016 Yasuhiro Matsumoto).
// Only RuneWidth is included; StringWidth, Truncate, Wrap, and locale
// detection are omitted. EastAsianWidth defaults to false.
package runewidth

type interval struct {
	first rune
	last  rune
}

type table []interval

func inTables(r rune, ts ...table) bool {
	for _, t := range ts {
		if inTable(r, t) {
			return true
		}
	}
	return false
}

func inTable(r rune, t table) bool {
	if r < t[0].first {
		return false
	}
	if r > t[len(t)-1].last {
		return false
	}

	bot := 0
	top := len(t) - 1
	for top >= bot {
		mid := (bot + top) >> 1

		switch {
		case t[mid].last < r:
			bot = mid + 1
		case t[mid].first > r:
			top = mid - 1
		default:
			return true
		}
	}

	return false
}

var nonprint = table{
	{0x0000, 0x001F}, {0x007F, 0x009F}, {0x00AD, 0x00AD},
	{0x070F, 0x070F}, {0x180B, 0x180E}, {0x200B, 0x200F},
	{0x2028, 0x202E}, {0x206A, 0x206F}, {0xD800, 0xDFFF},
	{0xFEFF, 0xFEFF}, {0xFFF9, 0xFFFB}, {0xFFFE, 0xFFFF},
}

// RuneWidth returns the number of cells in r.
// See http://www.unicode.org/reports/tr11/
func RuneWidth(r rune) int {
	if r < 0 || r > 0x10FFFF {
		return 0
	}
	switch {
	case r < 0x20:
		return 0
	case (r >= 0x7F && r <= 0x9F) || r == 0xAD:
		return 0
	case r < 0x300:
		return 1
	case inTable(r, narrow):
		return 1
	case inTables(r, nonprint, combining):
		return 0
	case inTable(r, doublewidth):
		return 2
	default:
		return 1
	}
}
