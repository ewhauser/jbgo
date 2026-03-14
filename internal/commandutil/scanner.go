package commandutil

func ScannerTokenLimit(maxFileBytes int64) int {
	const defaultTokenSize = 64 * 1024

	maxTokenSize := defaultTokenSize
	if maxFileBytes <= int64(defaultTokenSize) {
		return maxTokenSize
	}

	maxInt := int(^uint(0) >> 1)
	if maxFileBytes >= int64(maxInt) {
		return maxInt
	}
	return int(maxFileBytes)
}
