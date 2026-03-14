//go:build windows

package builtins_test

func defaultArchMachine() string {
	return archMachineFromGOARCH()
}
