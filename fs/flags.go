package fs

import "os"

func hasWriteIntent(flag int) bool {
	if canWrite(flag) {
		return true
	}
	return flag&(os.O_APPEND|os.O_CREATE|os.O_TRUNC) != 0
}
