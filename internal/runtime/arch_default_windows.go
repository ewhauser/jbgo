//go:build windows

package runtime

func defaultArchMachine() string {
	return archMachineFromGOARCH()
}
