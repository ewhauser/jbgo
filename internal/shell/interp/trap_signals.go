package interp

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/shell/syntax"
)

type trapID int

const (
	trapIDExit   trapID = 0
	trapIDDebug  trapID = -1
	trapIDErr    trapID = -2
	trapIDReturn trapID = -3
)

type trapDefaultDisposition uint8

const (
	trapDefaultTerminate trapDefaultDisposition = iota + 1
	trapDefaultIgnore
	trapDefaultStop
)

type trapSignalInfo struct {
	number             int
	name               string
	aliases            []string
	catchable          bool
	defaultDisposition trapDefaultDisposition
}

type trapActionKind uint8

const (
	trapActionDefault trapActionKind = iota
	trapActionIgnore
	trapActionCommand
)

type trapAction struct {
	kind    trapActionKind
	command string
}

type SignalInfo struct {
	Number int
	Name   string
}

func (a trapAction) active() bool {
	return a.kind != trapActionDefault
}

func (a trapAction) printable() string {
	switch a.kind {
	case trapActionIgnore:
		return ""
	case trapActionCommand:
		return a.command
	default:
		return ""
	}
}

var trapPseudoNames = map[trapID]string{
	trapIDExit:   "EXIT",
	trapIDDebug:  "DEBUG",
	trapIDErr:    "ERR",
	trapIDReturn: "RETURN",
}

var trapSignalByNumber map[int]trapSignalInfo
var trapSignalAlias map[string]trapSignalInfo
var trapSignalOrder []trapSignalInfo

func init() {
	trapSignalByNumber = make(map[int]trapSignalInfo, len(platformTrapSignals))
	trapSignalAlias = make(map[string]trapSignalInfo, len(platformTrapSignals)*3)
	trapSignalOrder = append([]trapSignalInfo(nil), platformTrapSignals...)
	slices.SortFunc(trapSignalOrder, func(a, b trapSignalInfo) int {
		return a.number - b.number
	})
	for _, info := range trapSignalOrder {
		trapSignalByNumber[info.number] = info
		trapSignalAlias[strings.ToUpper(info.name)] = info
		short := strings.TrimPrefix(strings.ToUpper(info.name), "SIG")
		trapSignalAlias[short] = info
		for _, alias := range info.aliases {
			trapSignalAlias[strings.ToUpper(alias)] = info
		}
	}
}

func trapPrintOrderIDs() []trapID {
	ids := make([]trapID, 0, len(trapSignalOrder)+4)
	ids = append(ids, trapIDExit)
	for _, info := range trapSignalOrder {
		ids = append(ids, trapID(info.number))
	}
	ids = append(ids, trapIDDebug, trapIDErr, trapIDReturn)
	return ids
}

func trapPrintName(id trapID) string {
	if name, ok := trapPseudoNames[id]; ok {
		return name
	}
	if info, ok := trapSignalByNumber[int(id)]; ok {
		return info.name
	}
	return strconv.Itoa(int(id))
}

func parseTrapUnsigned(value string) (int, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, false
	}
	return n, true
}

func resolveTrapID(raw string) (trapID, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("trap: %s: invalid signal specification", raw)
	}
	if n, ok := parseTrapUnsigned(trimmed); ok {
		if n == 0 {
			return trapIDExit, nil
		}
		if _, ok := trapSignalByNumber[n]; ok {
			return trapID(n), nil
		}
		return 0, fmt.Errorf("trap: %s: invalid signal specification", raw)
	}
	upper := strings.ToUpper(trimmed)
	for id, name := range trapPseudoNames {
		if upper == name {
			return id, nil
		}
	}
	info, ok := trapSignalAlias[upper]
	if !ok {
		return 0, fmt.Errorf("trap: %s: invalid signal specification", raw)
	}
	return trapID(info.number), nil
}

func trapSignalInfoByID(id trapID) (trapSignalInfo, bool) {
	info, ok := trapSignalByNumber[int(id)]
	return info, ok
}

func ResolveSignal(spec string) (SignalInfo, error) {
	id, err := resolveTrapID(spec)
	if err != nil {
		return SignalInfo{}, err
	}
	info, ok := trapSignalInfoByID(id)
	if !ok {
		return SignalInfo{}, fmt.Errorf("invalid signal specification: %s", spec)
	}
	return SignalInfo{Number: info.number, Name: strings.TrimPrefix(info.name, "SIG")}, nil
}

func ListSignals() []SignalInfo {
	out := make([]SignalInfo, 0, len(trapSignalOrder))
	for _, info := range trapSignalOrder {
		out = append(out, SignalInfo{Number: info.number, Name: strings.TrimPrefix(info.name, "SIG")})
	}
	return out
}

func trapQuotedCommand(command string) string {
	if command == "" {
		return "''"
	}
	if trapCanUseSingleQuotes(command) {
		return "'" + command + "'"
	}
	return bashDeclPlainValue(syntax.LangBash, command)
}

func trapCanUseSingleQuotes(command string) bool {
	for i := 0; i < len(command); i++ {
		switch c := command[i]; {
		case c == '\'':
			return false
		case c == '\n':
			continue
		case c < 0x20 || c == 0x7f:
			return false
		}
	}
	return true
}
