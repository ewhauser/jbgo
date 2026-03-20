package builtins

import (
	"fmt"
	stdfs "io/fs"
	"strings"
)

func fileTypeName(info stdfs.FileInfo) string {
	switch {
	case info.Mode()&stdfs.ModeSymlink != 0:
		return "symbolic link"
	case info.IsDir():
		return "directory"
	default:
		return "regular file"
	}
}

func formatModeOctal(mode stdfs.FileMode) string {
	return fmt.Sprintf("%04o", mode&(stdfs.ModePerm|stdfs.ModeSetuid|stdfs.ModeSetgid|stdfs.ModeSticky))
}

func formatModeOctalBare(mode stdfs.FileMode) string {
	return fmt.Sprintf("%o", mode&(stdfs.ModePerm|stdfs.ModeSetuid|stdfs.ModeSetgid|stdfs.ModeSticky))
}

func formatModeLong(mode stdfs.FileMode) string {
	var b strings.Builder
	switch {
	case mode&stdfs.ModeSymlink != 0:
		b.WriteByte('l')
	case mode.IsDir():
		b.WriteByte('d')
	default:
		b.WriteByte('-')
	}

	triples := []struct {
		read, write, exec stdfs.FileMode
		special           stdfs.FileMode
		execOn, execOff   byte
	}{
		{0o400, 0o200, 0o100, stdfs.ModeSetuid, 's', 'S'},
		{0o040, 0o020, 0o010, stdfs.ModeSetgid, 's', 'S'},
		{0o004, 0o002, 0o001, stdfs.ModeSticky, 't', 'T'},
	}
	for _, triple := range triples {
		if mode&triple.read != 0 {
			b.WriteByte('r')
		} else {
			b.WriteByte('-')
		}
		if mode&triple.write != 0 {
			b.WriteByte('w')
		} else {
			b.WriteByte('-')
		}
		switch {
		case mode&triple.special != 0 && mode&triple.exec != 0:
			b.WriteByte(triple.execOn)
		case mode&triple.special != 0:
			b.WriteByte(triple.execOff)
		case mode&triple.exec != 0:
			b.WriteByte('x')
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func humanizeBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}
	value := float64(size)
	suffixes := []string{"K", "M", "G", "T"}
	for _, suffix := range suffixes {
		value /= unit
		if value < unit {
			if value >= 10 {
				return fmt.Sprintf("%.0f%s", value, suffix)
			}
			return fmt.Sprintf("%.1f%s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1fP", value/unit)
}

func joinChildPath(parent, child string) string {
	switch parent {
	case "", ".":
		return child
	case "/":
		return "/" + child
	default:
		return parent + "/" + child
	}
}
