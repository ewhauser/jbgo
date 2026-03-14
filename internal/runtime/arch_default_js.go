//go:build js

package runtime

func defaultArchMachine() string {
	return archMachineFromGOARCH()
}
