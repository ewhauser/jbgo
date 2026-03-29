package interp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/shell/syntax"
	"github.com/ewhauser/gbash/shellvariant"
)

func (r *Runner) trapActions() map[trapID]trapAction {
	if r.traps.actions == nil {
		r.traps.actions = make(map[trapID]trapAction, 8)
	}
	return r.traps.actions
}

func (r *Runner) trapPrintAction(id trapID) trapAction {
	if action := r.trapAction(id); action.active() {
		return action
	}
	if r == nil || r.traps.display == nil {
		return trapAction{}
	}
	return r.traps.display[id]
}

func (r *Runner) trapPrintActions() map[trapID]trapAction {
	if r == nil {
		return nil
	}
	if len(r.traps.display) == 0 && len(r.traps.actions) == 0 {
		return nil
	}
	actions := make(map[trapID]trapAction, len(r.traps.display)+len(r.traps.actions))
	for id, action := range r.traps.display {
		if action.active() {
			actions[id] = action
		}
	}
	for id, action := range r.traps.actions {
		if action.active() {
			actions[id] = action
		}
	}
	if len(actions) == 0 {
		return nil
	}
	return actions
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
	r.traps.generation++
	if r.traps.display != nil {
		r.traps.display = nil
	}
	if !action.active() {
		if r.traps.actions != nil {
			delete(r.traps.actions, id)
		}
		return
	}
	r.trapActions()[id] = action
}

func (r *Runner) cloneTrapStateFrom(parent *Runner) {
	if parent == nil {
		r.traps.actions = nil
		r.traps.display = nil
		return
	}
	if len(parent.traps.actions) == 0 && len(parent.traps.display) == 0 {
		r.traps.actions = nil
		r.traps.display = nil
		return
	}
	display := parent.trapPrintActions()
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
	} else {
		r.traps.actions = actions
	}
	for id := range actions {
		delete(display, id)
	}
	if len(display) == 0 {
		r.traps.display = nil
		return
	}
	r.traps.display = display
}

func (r *Runner) validateTrapCommand(command string) error {
	if _, err := r.newParser().Parse(strings.NewReader(command), "trap"); err != nil {
		return err
	}
	return nil
}

func trimTrapParseError(err syntax.ParseError, variant shellvariant.ShellVariant) string {
	lines := strings.Split(formatParseError(err, variant), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "line ") {
			_, text, ok := strings.Cut(line, ": ")
			if ok {
				lines[i] = text
			}
			continue
		}
		prefix, rest, ok := strings.Cut(line, ": line ")
		if !ok || prefix == "" {
			continue
		}
		_, text, ok := strings.Cut(rest, ": ")
		if !ok {
			continue
		}
		lines[i] = text
	}
	return strings.Join(lines, "\n")
}

func trapInvalidOptionUsageLine() string {
	if runtime.GOOS == "darwin" {
		return "trap: usage: trap [-lp] [arg signal_spec ...]\n"
	}
	return "trap: usage: trap [-lp] [[arg] signal_spec ...]\n"
}

func (r *Runner) trapBuiltin(ctx context.Context, args []string) (exit exitStatus) {
	failf := func(code uint8, format string, args ...any) exitStatus {
		r.errf(format, args...)
		exit.code = code
		return exit
	}
	usagef := func() exitStatus {
		r.errf("trap: usage: trap [-Plp] [[action] signal_spec ...]\n")
		exit.code = 2
		return exit
	}
	invalidOptionUsagef := func() exitStatus {
		r.errf("%s", trapInvalidOptionUsageLine())
		exit.code = 2
		return exit
	}

	printMode := false
	listMode := false
	argIdx := 0
flagLoop:
	for argIdx < len(args) {
		arg := args[argIdx]
		switch {
		case arg == "--":
			argIdx++
			break flagLoop
		case arg == "":
			break flagLoop
		case arg == "-":
			break flagLoop
		case arg[0] != '-':
			break flagLoop
		case len(arg) == 1:
			break flagLoop
		default:
			for i := 1; i < len(arg); i++ {
				switch arg[i] {
				case 'p':
					printMode = true
				case 'l':
					listMode = true
				default:
					r.errf("trap: %s: invalid option\n", arg)
					return invalidOptionUsagef()
				}
			}
			argIdx++
		}
	}
	args = args[argIdx:]

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
			action := r.trapPrintAction(id)
			if !action.active() {
				continue
			}
			r.outf("trap -- %s %s\n", trapQuotedCommand(r.parserLangVariant(), action.printable()), trapPrintName(id))
		}
		return exit
	}

	if len(args) == 0 {
		return exit
	}

	callback := ""
	var signalArgs []string
	switch {
	case len(args) == 1:
		if _, err := resolveTrapID(args[0]); err != nil {
			return usagef()
		}
		callback = "-"
		signalArgs = args
	case args[0] == "-":
		callback = "-"
		signalArgs = args[1:]
	case isTrapResetCommand(args[0]):
		callback = "-"
		signalArgs = args[1:]
		if _, err := resolveTrapID(args[0]); err == nil {
			signalArgs = args
		}
	default:
		callback = args[0]
		signalArgs = args[1:]
	}
	if len(signalArgs) == 0 {
		return usagef()
	}

	action := trapAction{}
	switch callback {
	case "-":
		action = trapAction{kind: trapActionDefault}
	case "":
		action = trapAction{kind: trapActionIgnore}
	default:
		action = trapAction{kind: trapActionCommand, command: callback}
	}

	for _, raw := range signalArgs {
		id, err := resolveTrapID(raw)
		if err != nil {
			return failf(1, "%v\n", err)
		}
		r.setTrapAction(id, action)
	}
	return exit
}

func isTrapResetCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	return trimmed == "-" || hasUnsignedResetPrefix(command)
}

func hasUnsignedResetPrefix(command string) bool {
	if strings.TrimSpace(command) != command {
		return false
	}
	_, ok := parseTrapUnsigned(command)
	return ok
}

type trapRunResult struct {
	handler exitStatus
	ran     bool
}

func trapUsesLineOverride(id trapID) bool {
	switch id {
	case trapIDDebug, trapIDErr, trapIDReturn:
		return true
	default:
		return false
	}
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
	r.trapLineOverride = 0
	if trapUsesLineOverride(id) {
		r.trapLineOverride = line
	}
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
		switch {
		case errors.As(err, &statusErr):
		default:
			var parseErr syntax.ParseError
			if errors.As(err, &parseErr) {
				if parseErr.SourceLine == "" && parseErr.WantsSourceLine() {
					parseErr.SourceLine = action.command
				}
				r.errf("%s\n", trimTrapParseError(parseErr, r.shellVariantName()))
				handler.code = 2
				handler.err = parseErr
				break
			}
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
	pending := append([]pendingSignalTrap(nil), r.traps.pending...)
	r.traps.pending = r.traps.pending[:0]
	for _, trap := range pending {
		_ = r.runTrap(ctx, trap.id, trap.line, trap.status)
	}
}

func (r *Runner) queueSignalTrap(id trapID) {
	r.traps.pending = append(r.traps.pending, pendingSignalTrap{
		id:     id,
		status: r.exit.code,
		line:   1,
	})
}

func (r *Runner) dispatchSignal(target string, number int) error {
	if strings.HasPrefix(target, "g") {
		return r.dispatchOwnedSignal(r, target, number)
	}
	owner := r
	if owner.signalOwner != nil {
		owner = owner.signalOwner
	}
	return owner.dispatchOwnedSignal(r, target, number)
}

func (r *Runner) DispatchSignal(target string, number int) error {
	return r.dispatchSignal(target, number)
}

func (r *Runner) dispatchOwnedSignal(caller *Runner, target string, number int) error {
	info, ok := trapSignalByNumber[number]
	if !ok {
		return fmt.Errorf("invalid signal %d", number)
	}
	resolveTarget := func(target *Runner, caller *Runner) *Runner {
		if target == nil {
			return nil
		}
		id := trapID(number)
		if action := target.trapAction(id); action.active() {
			return target
		}
		switch number {
		case 2, 3:
			return target
		}
		if caller != nil && caller != target {
			owner := caller
			if owner.signalOwner != nil {
				owner = owner.signalOwner
			}
			if owner == r {
				if action := caller.trapAction(id); action.active() {
					return caller
				}
			}
		}
		if child := target.signalChildTrapTarget(id); child != nil {
			if action := child.trapAction(id); action.active() {
				return child
			}
		}
		return target
	}
	switch target {
	case strconv.Itoa(r.pid), strconv.Itoa(r.bashPID):
		targetRunner := resolveTarget(r, caller)
		if action := targetRunner.trapAction(trapID(number)); action.kind == trapActionIgnore {
			return nil
		}
		if info.catchable && targetRunner.trapAction(trapID(number)).kind == trapActionCommand {
			targetRunner.queueSignalTrap(trapID(number))
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
		targetRunner := resolveTarget(bg.runner, nil)
		if targetRunner == nil {
			return fmt.Errorf("%s: arguments must be process or job IDs", target)
		}
		if action := targetRunner.trapAction(trapID(number)); action.kind == trapActionIgnore {
			return nil
		}
		if info.catchable && targetRunner.trapAction(trapID(number)).kind == trapActionCommand {
			targetRunner.queueSignalTrap(trapID(number))
		}
		return nil
	}
}

func (r *Runner) runDebugTrap(ctx context.Context, line uint) bool {
	if !r.debugTrapAllowed() {
		return false
	}
	line = r.currentVisibleLine(line)
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
	line = r.currentVisibleLine(line)
	result := r.runTrap(ctx, trapIDReturn, line, status)
	if result.handler.exiting || result.handler.fatalExit {
		r.exit = result.handler
	}
}

func (r *Runner) runSourceReturnTrap(ctx context.Context, line uint, status uint8, preSourceGen uint64) {
	if !r.returnTrapAllowed() {
		// The caller frame (e.g. an untraced function) doesn't allow
		// inherited RETURN traps.  Still fire the trap if any trap was
		// set or reset during the source execution itself.
		if r.traps.generation == preSourceGen {
			return
		}
	}
	line = r.currentVisibleLine(line)
	result := r.runTrap(ctx, trapIDReturn, line, status)
	if result.handler.exiting || result.handler.fatalExit {
		r.exit = result.handler
	}
}

func (r *Runner) maybeRunErrTrap(ctx context.Context, line uint) {
	if r.noErrExit || r.exit.ok() {
		return
	}
	line = r.trapEffectiveLine(line)
	if r.errTrapAllowed() {
		line = r.currentVisibleLine(line)
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

func (r *Runner) trapEffectiveLine(line uint) uint {
	if r.trapLineOverride != 0 {
		return r.trapLineOverride
	}
	return line
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
	visible := r.trapPrintActions()
	ids := make([]trapID, 0, len(visible))
	for id := range visible {
		ids = append(ids, id)
	}
	sortedTrapIDs(ids)
	for _, id := range ids {
		action := r.trapPrintAction(id)
		if !action.active() {
			continue
		}
		if _, err := fmt.Fprintf(w, "trap -- %s %s\n", trapQuotedCommand(r.parserLangVariant(), action.printable()), trapPrintName(id)); err != nil {
			return err
		}
	}
	return nil
}
