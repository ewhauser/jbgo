package commands

func ScannerTokenLimit(inv *Invocation) int {
	const defaultTokenSize = 64 * 1024

	maxTokenSize := defaultTokenSize
	if inv == nil || inv.Limits.MaxFileBytes <= int64(defaultTokenSize) {
		return maxTokenSize
	}

	maxInt := int(^uint(0) >> 1)
	if inv.Limits.MaxFileBytes >= int64(maxInt) {
		return maxInt
	}
	return int(inv.Limits.MaxFileBytes)
}
