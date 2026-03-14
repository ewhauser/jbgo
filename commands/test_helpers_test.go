package commands

import "slices"

func equalStrings(got, want []string) bool {
	return slices.Equal(got, want)
}
