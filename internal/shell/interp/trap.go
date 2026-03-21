package interp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func (r *Runner) trapActions() map[trapID]trapAction {
	if r.traps.actions == nil {
		r.traps.actions = make(map[trapID]trapAction, 8)
	}
	return r.traps.actions
}

func (r *Runner) trapActiveCounts() map[trapID]int {
	if r.traps.active == nil {
		r.traps.active = make(map[trapID]int, 4)
	}
	return r.traps.active
}

func (r *Runner) trapAction(id trapID) trapAction {
	if r == nil || r.traps.actions == nil {
		return trapAction{}
	}
	return r.traps.actions[id]
}

func (r *Runner) setTrapAction(id trapID, action trapAction) {
	if !action.active() {
		if r.traps.actions != nil {
			delete(r.traps.actions, id)
		}
		return
	}
	r.trapActions()[id] = action
}

func (r *Runner) cloneTrapStateFrom(parent *Runner) {
	if parent == nil || len(parent.traps.actions) == 0 {
		r.traps.actions = nil
		return
	}
	actions := make(map[trapID]trapAction, len(parent.traps.actions))
	for id, action := range parent.traps.actions {
		switch {
		case id == trapIDExit:
			continue
		case id > 0:
			if action.kind == trapActionIgnore {
				actions[id] = action
			}
		case id == trapIDErr:
			if r.opts[optErrTrace] {
				actions[id] = action
			}
		case id == trapIDDebug:
			if r.opts[optFuncTrace] {
				actions[id] = action
			}
		case id == trapIDReturn:
			// RETURN is not inherited into subshell-like child runners.
		}
	}
	if len(actions) == 0 {
		r.traps.actions = nil
		return
	}
	r.traps.actions = actions
}

func (r *Runner) validateTrapCommand(command string) error {
	if _, err := r.newParser().Parse(strings.NewReader(command), "trap"); err != nil {
		return err
	}
	return nil
}

func (r *Runner) trapBuiltin(ctx context.Context, args []string) (exit exitStatus) {
	failf := func(code uint8, format string, args ...any) exitStatus {
		r.errf(format, args...)
		exit.code = code
		return exit
	}

	fp := flagParser{remaining: args}
	printMode := false
	listMode := false
	for fp.more() {
		switch flag := fp.flag(); flag {
		case "-p":
			printMode = true
		case "-l":
			listMode = true
		case "-":
			// reset form
		default:
			r.errf("trap: %q: invalid option\n", flag)
			r.errf("trap: usage: trap [-lp] [[arg] signal_spec ...]\n")
			exit.code = 2
			return exit
		}
	}
	args = fp.args()

	if listMode {
		if len(args) > 0 {
			for _, raw := range args {
				id, err := resolveTrapID(raw)
				if err != nil {
					return failf(2, "%v\n", err)
				}
				info, ok := trapSignalInfoByID(id)
				if !ok {
					continue
				}
				r.outf("%d) %s\n", info.number, info.name)
			}
			return exit
		}
		for _, info := range trapSignalOrder {
			r.outf("%d) %s\n", info.number, info.name)
		}
		return exit
	}

	printRequested := printMode || len(args) == 0
	if printRequested {
		var ids []trapID
		if len(args) > 0 {
			ids = make([]trapID, 0, len(args))
			for _, raw := range args {
				id, err := resolveTrapID(raw)
				if err != nil {
					return failf(2, "%v\n", err)
				}
				ids = append(ids, id)
			}
		} else {
			ids = trapPrintOrderIDs()
		}
		for _, id := range ids {
			action := r.trapAction(id)
			if !action.active() {
				continue
			}
			r.outf("trap -- %s %s\n", trapQuotedCommand(action.printable()), trapPrintName(id))
		}
		return exit
	}

	if len(args) == 0 {
		return exit
	}

	callback := "-"
	signalArgs := args
	switch {
	case len(args) == 1:
		// Single-arg form resets the named signal.
	case isTrapResetCommand(args[0]):
		signalArgs = args[1:]
	default:
		callback = args[0]
		signalArgs = args[1:]
	}
	if len(signalArgs) == 0 {
		r.errf("trap: usage: trap [-lp] [[arg] signal_spec ...]\n")
		exit.code = 2
		return exit
	}

	action := trapAction{}
	switch callback {
	case "-":
		action = trapAction{kind: trapActionDefault}
	case "":
		action = trapAction{kind: trapActionIgnore}
	default:
		if err := r.validateTrapCommand(callback); err != nil {
			exit.code = 1
			return exit
		}
		action = trapAction{kind: trapActionCommand, command: callback}
	}

	ids := make([]trapID, 0, len(signalArgs))
	for _, raw := range signalArgs {
		id, err := resolveTrapID(raw)
		if err != nil {
			return failf(2, "%v\n", err)
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		r.setTrapAction(id, action)
	}
	return exit
}

func isTrapResetCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	return trimmed == "-" || hasUnsignedResetPrefix(command)
}

func hasUnsignedResetPrefix(command string) bool {
	_, ok := parseTrapUnsigned(command)
	return ok
}

type trapRunResult struct {
	handler exitStatus
	ran     bool
}

func (r *Runner) runTrap(ctx context.Context, id trapID, line uint, status uint8) trapRunResult {
	action := r.trapAction(id)
	if !action.active() || action.kind != trapActionCommand {
		return trapRunResult{}
	}
	active := r.trapActiveCounts()
	if active[id] > 0 {
		return trapRunResult{}
	}
	active[id]++
	defer func() {
		active[id]--
		if active[id] == 0 {
			delete(active, id)
		}
	}()

	oldExit := r.exit
	oldLastExit := r.lastExit
	oldLine := r.trapLineOverride
	oldSignal := r.traps.currentSignalNumber
	r.lastExit = exitStatus{code: status}
	r.trapLineOverride = line
	if info, ok := trapSignalInfoByID(id); ok {
		r.traps.currentSignalNumber = info.number
	} else {
		r.traps.currentSignalNumber = 0
	}
	defer func() {
		r.lastExit = oldLastExit
		r.trapLineOverride = oldLine
		r.traps.currentSignalNumber = oldSignal
	}()

	name := strings.ToLower(trapPrintName(id))
	err := r.runShellReader(ctx, strings.NewReader(action.command), name+" trap", nil)
	handler := r.exit
	if err != nil {
		var statusErr ExitStatus
		if !errors.As(err, &statusErr) {
			r.errf("%s trap: %v\n", name, err)
			handler.fatal(err)
		}
	}

	if handler.exiting || handler.fatalExit {
		return trapRunResult{handler: handler, ran: true}
	}
	if id == trapIDExit {
		r.exit = oldExit
		return trapRunResult{handler: handler, ran: true}
	}
	r.exit = oldExit
	return trapRunResult{handler: handler, ran: true}
}

func (r *Runner) runPendingSignalTraps(ctx context.Context) {
	if len(r.traps.pending) == 0 {
		return
	}
	pending := append([]trapID(nil), r.traps.pending...)
	r.traps.pending = r.traps.pending[:0]
	for _, id := range pending {
		_ = r.runTrap(ctx, id, r.currentStmtLine, 128+signalExitCode(id))
	}
}

func signalExitCode(id trapID) uint8 {
	info, ok := trapSignalInfoByID(id)
	if !ok {
		return 0
	}
	return uint8(info.number)
}

func (r *Runner) queueSignalTrap(id trapID) {
	r.traps.pending = append(r.traps.pending, id)
}

func (r *Runner) dispatchSignal(target string, number int) error {
	owner := r
	if owner.signalOwner != nil {
		owner = owner.signalOwner
	}
	return owner.dispatchOwnedSignal(target, number)
}

func (r *Runner) DispatchSignal(target string, number int) error {
	return r.dispatchSignal(target, number)
}

func (r *Runner) dispatchOwnedSignal(target string, number int) error {
	info, ok := trapSignalByNumber[number]
	if !ok {
		return fmt.Errorf("invalid signal %d", number)
	}
	switch target {
	case strconv.Itoa(r.pid), strconv.Itoa(r.bashPID):
		if action := r.trapAction(trapID(number)); action.kind == trapActionIgnore {
			return nil
		}
		if info.catchable && r.trapAction(trapID(number)).kind == trapActionCommand {
			r.queueSignalTrap(trapID(number))
		}
		return nil
	default:
		if !strings.HasPrefix(target, "g") {
			return fmt.Errorf("%s: arguments must be process or job IDs", target)
		}
		idx, err := strconv.Atoi(strings.TrimPrefix(target, "g"))
		if err != nil || idx <= 0 || idx > len(r.bgProcs) {
			return fmt.Errorf("%s: arguments must be process or job IDs", target)
		}
		bg := r.bgProcs[idx-1]
		if bg.runner == nil {
			return fmt.Errorf("%s: arguments must be process or job IDs", target)
		}
		if action := bg.runner.trapAction(trapID(number)); action.kind == trapActionIgnore {
			return nil
		}
		if info.catchable && bg.runner.trapAction(trapID(number)).kind == trapActionCommand {
			bg.runner.queueSignalTrap(trapID(number))
		}
		return nil
	}
}

func (r *Runner) runDebugTrap(ctx context.Context, line uint) bool {
	if !r.debugTrapAllowed() {
		return false
	}
	result := r.runTrap(ctx, trapIDDebug, line, r.lastExit.code)
	if !result.ran {
		return false
	}
	if result.handler.exiting || result.handler.fatalExit {
		r.exit = result.handler
		return true
	}
	return r.opts[optExtDebug] && !result.handler.ok()
}

func (r *Runner) maybeRunReturnTrap(ctx context.Context, line uint, status uint8) {
	if !r.returnTrapAllowed() {
		return
	}
	result := r.runTrap(ctx, trapIDReturn, line, status)
	if result.handler.exiting || result.handler.fatalExit {
		r.exit = result.handler
	}
}

func (r *Runner) maybeRunErrTrap(ctx context.Context, line uint) {
	if r.noErrExit || r.exit.ok() {
		return
	}
	if r.errTrapAllowed() {
		result := r.runTrap(ctx, trapIDErr, line, r.exit.code)
		if result.handler.exiting || result.handler.fatalExit {
			r.exit = result.handler
			return
		}
	}
	if r.opts[optErrExit] {
		r.exit.exiting = true
	}
}

func debugLineForCommand(cm syntax.Command) uint {
	if cm == nil {
		return 0
	}
	return cm.Pos().Line()
}

func sortedTrapIDs(ids []trapID) {
	slices.SortFunc(ids, func(a, b trapID) int {
		order := func(id trapID) int {
			switch {
			case id == trapIDExit:
				return -1000
			case id > 0:
				return int(id)
			case id == trapIDDebug:
				return 1_000_000
			case id == trapIDErr:
				return 1_000_001
			case id == trapIDReturn:
				return 1_000_002
			default:
				return 1_000_100 + int(id)
			}
		}
		return order(a) - order(b)
	})
}

func (r *Runner) trapSignalNumber() string {
	if r.traps.currentSignalNumber == 0 {
		return ""
	}
	return strconv.Itoa(r.traps.currentSignalNumber)
}

func (r *Runner) trapSignalName() string {
	if r.traps.currentSignalNumber == 0 {
		return ""
	}
	return trapPrintName(trapID(r.traps.currentSignalNumber))
}

func (r *Runner) writeTrapList(w io.Writer) error {
	if w == nil {
		return nil
	}
	ids := make([]trapID, 0, len(r.traps.actions))
	for id := range r.traps.actions {
		ids = append(ids, id)
	}
	sortedTrapIDs(ids)
	for _, id := range ids {
		action := r.trapAction(id)
		if !action.active() {
			continue
		}
		if _, err := fmt.Fprintf(w, "trap -- %s %s\n", trapQuotedCommand(action.printable()), trapPrintName(id)); err != nil {
			return err
		}
	}
	return nil
}
