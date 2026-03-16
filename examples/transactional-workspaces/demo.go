package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/ewhauser/gbash"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/trace"
)

const (
	workspaceRoot     = "/workspace"
	mainWorkspaceName = "main"
	expectedValidRows = 9
)

const cleanScript = `#!/usr/bin/env bash
set -eu

workspace=/workspace
mkdir -p "$workspace/clean" "$workspace/archive" "$workspace/reports" "$workspace/tmp"

processed=0
kept=0

for file in "$workspace"/raw/*.csv; do
  base=$(basename "$file" .csv)

  {
    head -n 1 "$file"
    tail -n +2 "$file" \
      | tr '[:upper:]' '[:lower:]' \
      | grep ',ok$' \
      | sort
  } > "$workspace/tmp/${base}.cleaned.csv"

  rows=$(tail -n +2 "$workspace/tmp/${base}.cleaned.csv" | wc -l | tr -d ' ')
  kept=$((kept + rows))
  processed=$((processed + 1))

  mv "$workspace/tmp/${base}.cleaned.csv" "$workspace/clean/${base}.csv"
done

{
  echo '# Cleanup Summary'
  echo
  echo "- processed files: $processed"
  echo "- kept rows: $kept"
  echo "- filter rule: status=ok"
  echo "- note: warn rows are valid rows for this dataset."
} > "$workspace/reports/summary.md"

for file in "$workspace"/raw/*.csv; do
  mv "$file" "$workspace/archive/"
done

	rm -rf "$workspace/tmp"
`

func runDemo(ctx context.Context, stdin io.Reader, stdout, _ io.Writer, opts demoOptions) error {
	manager := newWorkspaceManager(workspaceRoot)
	if err := manager.seed(ctx); err != nil {
		return err
	}

	demo := scriptedDemo{
		out:        stdout,
		quiet:      opts.quiet,
		pause:      opts.pause,
		pauseInput: bufio.NewReader(stdin),
		styles:     newDemoStyles(opts.color),
		manager:    manager,
	}
	return demo.run(ctx)
}

type scriptedDemo struct {
	out        io.Writer
	quiet      bool
	pause      bool
	pauseInput *bufio.Reader
	styles     demoStyles
	manager    *workspaceManager
}

func (d scriptedDemo) run(ctx context.Context) error {
	if err := d.showInitialState(ctx); err != nil {
		return err
	}
	d.pauseForBeat("Next: checkpoint the workspace before the destructive script runs.")
	if err := d.createSnapshot(ctx, "before-cleanup"); err != nil {
		return err
	}
	d.pauseForBeat("Next: run the buggy cleanup and inspect exactly what it changed.")
	if err := d.runBuggyCleanup(ctx); err != nil {
		return err
	}
	d.pauseForBeat("Next: roll the workspace back to the saved snapshot.")
	if err := d.restoreMain(ctx, "before-cleanup"); err != nil {
		return err
	}
	d.pauseForBeat("Next: fork two independent workspaces from the same snapshot.")
	if err := d.branchFixes(ctx, "before-cleanup"); err != nil {
		return err
	}
	d.pauseForBeat("Next: run both repair strategies side by side and compare them.")
	if err := d.compareBranches(ctx, "before-cleanup"); err != nil {
		return err
	}
	d.pauseForBeat("Next: promote the winning branch back into main.")
	return d.promoteBranch(ctx, "before-cleanup", "fix-filter")
}

func (d scriptedDemo) showInitialState(ctx context.Context) error {
	d.section("Initial Workspace")
	d.line("This workspace starts with raw CSVs, a risky cleanup script, and no generated outputs.")
	d.command("tree /workspace")
	tree, err := d.manager.renderWorkspaceTree(ctx, mainWorkspaceName)
	if err != nil {
		return err
	}
	d.block(tree)
	if d.quiet {
		return nil
	}
	d.command("sed -n '1,40p' /workspace/scripts/clean.sh")
	snippet, err := d.manager.renderFile(ctx, mainWorkspaceName, "/workspace/scripts/clean.sh", 18)
	if err != nil {
		return err
	}
	d.block(snippet)
	return nil
}

func (d scriptedDemo) createSnapshot(ctx context.Context, name string) error {
	d.section("Create Snapshot")
	d.command("snapshot create " + name)
	if err := d.manager.snapshot(ctx, name, mainWorkspaceName); err != nil {
		return err
	}
	d.line("Saved a point-in-time copy of the workspace before the cleanup run.")
	return nil
}

func (d scriptedDemo) runBuggyCleanup(ctx context.Context) error {
	d.section("Run Buggy Cleanup")
	d.command("workspace run main -- /workspace/scripts/clean.sh")
	result, err := d.manager.runFile(ctx, mainWorkspaceName, "cleanup-buggy", "/workspace/scripts/clean.sh")
	if err != nil {
		return err
	}
	d.success(fmt.Sprintf("cleanup exited with code %d", result.ExitCode))

	d.command("tree /workspace")
	tree, err := d.manager.renderWorkspaceTree(ctx, mainWorkspaceName)
	if err != nil {
		return err
	}
	d.block(tree)

	d.command("cat /workspace/reports/summary.md")
	summary, err := d.manager.renderFile(ctx, mainWorkspaceName, "/workspace/reports/summary.md", 12)
	if err != nil {
		return err
	}
	d.block(summary)

	d.command("workspace diff before-cleanup --workspace main")
	diff, err := d.manager.renderDiffAgainstSnapshot(ctx, "before-cleanup", mainWorkspaceName)
	if err != nil {
		return err
	}
	d.block(diff)

	d.command("workspace trace main")
	d.block(renderMutationJournal(result.Events))
	d.warning(fmt.Sprintf("The seeded dataset has %d non-invalid rows, so the summary is obviously wrong.", expectedValidRows))
	return nil
}

func (d scriptedDemo) restoreMain(ctx context.Context, snapshotName string) error {
	d.section("Rollback")
	d.command("workspace restore " + snapshotName)
	if err := d.manager.restore(ctx, mainWorkspaceName, snapshotName); err != nil {
		return err
	}
	d.command("tree /workspace")
	tree, err := d.manager.renderWorkspaceTree(ctx, mainWorkspaceName)
	if err != nil {
		return err
	}
	d.block(tree)
	d.success("The workspace is back to the exact pre-run state without any cleanup script archeology.")
	return nil
}

func (d scriptedDemo) branchFixes(ctx context.Context, snapshotName string) error {
	d.section("Fork Branches")
	d.command("workspace fork " + snapshotName + " fix-filter")
	if err := d.manager.fork(ctx, snapshotName, "fix-filter"); err != nil {
		return err
	}

	d.command("workspace fork " + snapshotName + " keep-everything")
	if err := d.manager.fork(ctx, snapshotName, "keep-everything"); err != nil {
		return err
	}

	d.command("workspace graph")
	d.block(d.manager.renderGraph())

	d.section("Patch Branches")
	d.command("patch fix-filter /workspace/scripts/clean.sh")
	if err := d.manager.replaceInFile(ctx, "fix-filter", "| grep ',ok$' \\\n", "| grep -Ev ',invalid$' \\\n"); err != nil {
		return err
	}
	if err := d.manager.replaceInFile(ctx, "fix-filter", "status=ok", "status!=invalid"); err != nil {
		return err
	}

	d.command("patch keep-everything /workspace/scripts/clean.sh")
	if err := d.manager.replaceInFile(ctx, "keep-everything", "| grep ',ok$' \\\n", "| cat \\\n"); err != nil {
		return err
	}
	if err := d.manager.replaceInFile(ctx, "keep-everything", "status=ok", "all rows"); err != nil {
		return err
	}

	if d.quiet {
		return nil
	}

	d.command("sed -n '10,30p' /workspace/scripts/clean.sh  # fix-filter")
	fixed, err := d.manager.renderFile(ctx, "fix-filter", "/workspace/scripts/clean.sh", 18)
	if err != nil {
		return err
	}
	d.block(fixed)

	d.command("sed -n '10,30p' /workspace/scripts/clean.sh  # keep-everything")
	bypass, err := d.manager.renderFile(ctx, "keep-everything", "/workspace/scripts/clean.sh", 18)
	if err != nil {
		return err
	}
	d.block(bypass)
	return nil
}

func (d scriptedDemo) compareBranches(ctx context.Context, snapshotName string) error {
	d.section("Run Branches")
	fixResult, err := d.manager.runFile(ctx, "fix-filter", "cleanup-fix-filter", "/workspace/scripts/clean.sh")
	if err != nil {
		return err
	}
	keepResult, err := d.manager.runFile(ctx, "keep-everything", "cleanup-keep-everything", "/workspace/scripts/clean.sh")
	if err != nil {
		return err
	}

	fixRows, fixRule, err := d.manager.summaryMetrics(ctx, "fix-filter")
	if err != nil {
		return err
	}
	keepRows, keepRule, err := d.manager.summaryMetrics(ctx, "keep-everything")
	if err != nil {
		return err
	}

	d.command("workspace compare fix-filter keep-everything")
	d.block(fmt.Sprintf(
		"fix-filter: kept rows=%s, rule=%s, mutations=%d\nkeep-everything: kept rows=%s, rule=%s, mutations=%d",
		fixRows,
		fixRule,
		countMutations(fixResult.Events),
		keepRows,
		keepRule,
		countMutations(keepResult.Events),
	))

	d.command("workspace diff " + snapshotName + " --workspace fix-filter")
	fixDiff, err := d.manager.renderDiffAgainstSnapshot(ctx, snapshotName, "fix-filter")
	if err != nil {
		return err
	}
	d.block(fixDiff)

	d.command("workspace diff " + snapshotName + " --workspace keep-everything")
	keepDiff, err := d.manager.renderDiffAgainstSnapshot(ctx, snapshotName, "keep-everything")
	if err != nil {
		return err
	}
	d.block(keepDiff)

	if d.quiet {
		d.success("fix-filter is the winner: it keeps the expected rows while still removing invalid records.")
		return nil
	}

	d.command("cat /workspace/reports/summary.md  # fix-filter")
	fixSummary, err := d.manager.renderFile(ctx, "fix-filter", "/workspace/reports/summary.md", 12)
	if err != nil {
		return err
	}
	d.block(fixSummary)

	d.command("cat /workspace/reports/summary.md  # keep-everything")
	keepSummary, err := d.manager.renderFile(ctx, "keep-everything", "/workspace/reports/summary.md", 12)
	if err != nil {
		return err
	}
	d.block(keepSummary)

	d.success("fix-filter is the winner: it keeps the expected rows while still removing invalid records.")
	return nil
}

func (d scriptedDemo) promoteBranch(ctx context.Context, snapshotName, branch string) error {
	d.section("Promote Winning Branch")
	d.command("workspace merge " + branch)
	if err := d.manager.merge(ctx, branch, mainWorkspaceName); err != nil {
		return err
	}

	d.command("cat /workspace/reports/summary.md")
	summary, err := d.manager.renderFile(ctx, mainWorkspaceName, "/workspace/reports/summary.md", 12)
	if err != nil {
		return err
	}
	d.block(summary)

	d.command("workspace diff " + snapshotName + " --workspace main")
	diff, err := d.manager.renderDiffAgainstSnapshot(ctx, snapshotName, mainWorkspaceName)
	if err != nil {
		return err
	}
	d.block(diff)
	d.success("main now points at the accepted branch state.")
	return nil
}

func (d scriptedDemo) section(title string) {
	_, _ = fmt.Fprintf(d.out, "\n%s\n", d.styles.section("== "+title+" =="))
}

func (d scriptedDemo) line(text string) {
	_, _ = fmt.Fprintln(d.out, d.styles.body(text))
}

func (d scriptedDemo) success(text string) {
	_, _ = fmt.Fprintln(d.out, d.styles.success(text))
}

func (d scriptedDemo) warning(text string) {
	_, _ = fmt.Fprintln(d.out, d.styles.warning(text))
}

func (d scriptedDemo) command(cmd string) {
	_, _ = fmt.Fprintf(d.out, "%s\n", d.styles.command("$ "+cmd))
}

func (d scriptedDemo) block(text string) {
	if strings.TrimSpace(text) == "" {
		_, _ = fmt.Fprintln(d.out, d.styles.muted("(empty)"))
		return
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = d.styles.blockLine(line)
	}
	_, _ = fmt.Fprintln(d.out, strings.Join(lines, "\n"))
}

func (d scriptedDemo) pauseForBeat(text string) {
	if !d.pause {
		return
	}
	_, _ = fmt.Fprintln(d.out, d.styles.pause(text))
	_, err := d.pauseInput.ReadString('\n')
	if err != nil && err != io.EOF {
		_, _ = fmt.Fprintln(d.out, d.styles.warning("pause skipped: "+err.Error()))
	}
}

type demoStyles struct {
	enabled bool
}

func newDemoStyles(enabled bool) demoStyles {
	return demoStyles{enabled: enabled}
}

func (s demoStyles) section(text string) string {
	return s.wrap("1;36", text)
}

func (s demoStyles) body(text string) string {
	return s.wrap("0;37", text)
}

func (s demoStyles) command(text string) string {
	return s.wrap("1;33", text)
}

func (s demoStyles) success(text string) string {
	return s.wrap("1;32", text)
}

func (s demoStyles) warning(text string) string {
	return s.wrap("1;31", text)
}

func (s demoStyles) pause(text string) string {
	return s.wrap("1;35", "[pause] "+text+" Press Enter to continue.")
}

func (s demoStyles) muted(text string) string {
	return s.wrap("2", text)
}

func (s demoStyles) added(text string) string {
	return s.wrap("32", text)
}

func (s demoStyles) removed(text string) string {
	return s.wrap("31", text)
}

func (s demoStyles) changed(text string) string {
	return s.wrap("33", text)
}

func (s demoStyles) blockLine(text string) string {
	switch {
	case strings.HasPrefix(text, "+ "):
		return s.added(text)
	case strings.HasPrefix(text, "- "):
		return s.removed(text)
	case strings.HasPrefix(text, "~ "):
		return s.changed(text)
	case strings.HasPrefix(text, "fix-filter:"):
		return s.success(text)
	case strings.HasPrefix(text, "keep-everything:"):
		return s.warning(text)
	default:
		return text
	}
}

func (s demoStyles) wrap(code, text string) string {
	if !s.enabled || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

type workspaceManager struct {
	workDir    string
	workspaces map[string]*managedWorkspace
	snapshots  map[string]*managedSnapshot
}

type managedWorkspace struct {
	name           string
	session        *gbash.Session
	originSnapshot string
}

type managedSnapshot struct {
	name          string
	fromWorkspace string
	fs            gbfs.FileSystem
}

func newWorkspaceManager(workDir string) *workspaceManager {
	return &workspaceManager{
		workDir:    gbfs.Clean(workDir),
		workspaces: make(map[string]*managedWorkspace),
		snapshots:  make(map[string]*managedSnapshot),
	}
}

func (m *workspaceManager) seed(ctx context.Context) error {
	mainWS, err := m.newWorkspace(ctx, mainWorkspaceName, nil, "")
	if err != nil {
		return err
	}
	m.workspaces[mainWorkspaceName] = mainWS

	files := map[string]string{
		"/workspace/README.md": `# Transactional Workspace Demo

This lab contains 12 input rows.
Expected valid rows after cleanup: 9.

The bug is that warn rows are valid, but the cleanup script currently drops them.
`,
		"/workspace/raw/january.csv": `customer,region,amount,status
acme,west,120,ok
beacon,east,95,warn
cosmo,central,0,invalid
delta,west,140,ok
`,
		"/workspace/raw/february.csv": `customer,region,amount,status
ember,south,130,warn
fulton,east,210,ok
glacier,west,0,invalid
harbor,north,155,warn
`,
		"/workspace/raw/march.csv": `customer,region,amount,status
ion,central,180,ok
jupiter,west,80,warn
kepler,east,0,invalid
lumen,south,205,ok
`,
		"/workspace/scripts/clean.sh": cleanScript,
	}

	for name, data := range files {
		perm := stdfs.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			perm = 0o755
		}
		if err := writeSandboxFile(ctx, mainWS.session.FileSystem(), name, data, perm); err != nil {
			return err
		}
	}
	return nil
}

func (m *workspaceManager) snapshot(ctx context.Context, name, workspaceName string) error {
	workspace, ok := m.workspaces[workspaceName]
	if !ok {
		return fmt.Errorf("unknown workspace %q", workspaceName)
	}
	snapshotFS, err := gbfs.NewSnapshot(ctx, workspace.session.FileSystem())
	if err != nil {
		return fmt.Errorf("snapshot %s: %w", name, err)
	}
	m.snapshots[name] = &managedSnapshot{
		name:          name,
		fromWorkspace: workspaceName,
		fs:            snapshotFS,
	}
	return nil
}

func (m *workspaceManager) restore(ctx context.Context, workspaceName, snapshotName string) error {
	snapshot, ok := m.snapshots[snapshotName]
	if !ok {
		return fmt.Errorf("unknown snapshot %q", snapshotName)
	}
	workspace, err := m.newWorkspace(ctx, workspaceName, snapshot.fs, snapshotName)
	if err != nil {
		return err
	}
	m.workspaces[workspaceName] = workspace
	return nil
}

func (m *workspaceManager) fork(ctx context.Context, snapshotName, workspaceName string) error {
	snapshot, ok := m.snapshots[snapshotName]
	if !ok {
		return fmt.Errorf("unknown snapshot %q", snapshotName)
	}
	workspace, err := m.newWorkspace(ctx, workspaceName, snapshot.fs, snapshotName)
	if err != nil {
		return err
	}
	m.workspaces[workspaceName] = workspace
	return nil
}

func (m *workspaceManager) merge(ctx context.Context, sourceWorkspace, destWorkspace string) error {
	workspace, ok := m.workspaces[sourceWorkspace]
	if !ok {
		return fmt.Errorf("unknown workspace %q", sourceWorkspace)
	}
	mergedSnapshot, err := gbfs.NewSnapshot(ctx, workspace.session.FileSystem())
	if err != nil {
		return fmt.Errorf("snapshot merge source %s: %w", sourceWorkspace, err)
	}
	merged, err := m.newWorkspace(ctx, destWorkspace, mergedSnapshot, workspace.originSnapshot)
	if err != nil {
		return err
	}
	m.workspaces[destWorkspace] = merged
	return nil
}

func (m *workspaceManager) run(ctx context.Context, workspaceName, name, script string) (*gbash.ExecutionResult, error) {
	workspace, ok := m.workspaces[workspaceName]
	if !ok {
		return nil, fmt.Errorf("unknown workspace %q", workspaceName)
	}
	result, err := workspace.session.Exec(ctx, &gbash.ExecutionRequest{
		Name:   name,
		Script: script,
	})
	if err != nil {
		return nil, fmt.Errorf("run %s: %w", workspaceName, err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("%s exited with code %d: %s", workspaceName, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return result, nil
}

func (m *workspaceManager) runFile(ctx context.Context, workspaceName, name, filePath string) (*gbash.ExecutionResult, error) {
	script, err := m.readFile(ctx, workspaceName, filePath)
	if err != nil {
		return nil, err
	}
	return m.run(ctx, workspaceName, name, script)
}

func (m *workspaceManager) replaceInFile(ctx context.Context, workspaceName, old, replacement string) error {
	const filePath = "/workspace/scripts/clean.sh"
	workspace, ok := m.workspaces[workspaceName]
	if !ok {
		return fmt.Errorf("unknown workspace %q", workspaceName)
	}
	current, err := readSandboxFile(ctx, workspace.session.FileSystem(), filePath)
	if err != nil {
		return err
	}
	if !strings.Contains(current, old) {
		return fmt.Errorf("%s does not contain %q", filePath, strings.TrimSpace(old))
	}
	updated := strings.Replace(current, old, replacement, 1)
	return writeSandboxFile(ctx, workspace.session.FileSystem(), filePath, updated, 0o755)
}

func (m *workspaceManager) summaryMetrics(ctx context.Context, workspaceName string) (rows, rule string, err error) {
	content, err := m.readFile(ctx, workspaceName, "/workspace/reports/summary.md")
	if err != nil {
		return "", "", err
	}
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "- kept rows:"):
			rows = strings.TrimSpace(strings.TrimPrefix(line, "- kept rows:"))
		case strings.HasPrefix(line, "- filter rule:"):
			rule = strings.TrimSpace(strings.TrimPrefix(line, "- filter rule:"))
		}
	}
	if rows == "" || rule == "" {
		return "", "", fmt.Errorf("summary metrics missing in %s", workspaceName)
	}
	return rows, rule, nil
}

func (m *workspaceManager) renderWorkspaceTree(ctx context.Context, workspaceName string) (string, error) {
	workspace, ok := m.workspaces[workspaceName]
	if !ok {
		return "", fmt.Errorf("unknown workspace %q", workspaceName)
	}
	return renderTree(ctx, workspace.session.FileSystem(), m.workDir)
}

func (m *workspaceManager) renderFile(ctx context.Context, workspaceName, filePath string, maxLines int) (string, error) {
	content, err := m.readFile(ctx, workspaceName, filePath)
	if err != nil {
		return "", err
	}
	return renderSnippet(content, maxLines), nil
}

func (m *workspaceManager) readFile(ctx context.Context, workspaceName, filePath string) (string, error) {
	workspace, ok := m.workspaces[workspaceName]
	if !ok {
		return "", fmt.Errorf("unknown workspace %q", workspaceName)
	}
	return readSandboxFile(ctx, workspace.session.FileSystem(), filePath)
}

func (m *workspaceManager) renderDiffAgainstSnapshot(ctx context.Context, snapshotName, workspaceName string) (string, error) {
	snapshot, ok := m.snapshots[snapshotName]
	if !ok {
		return "", fmt.Errorf("unknown snapshot %q", snapshotName)
	}
	workspace, ok := m.workspaces[workspaceName]
	if !ok {
		return "", fmt.Errorf("unknown workspace %q", workspaceName)
	}
	return renderDiff(ctx, snapshot.fs, workspace.session.FileSystem(), m.workDir)
}

func (m *workspaceManager) renderGraph() string {
	snapshotNames := make([]string, 0, len(m.snapshots))
	for name := range m.snapshots {
		snapshotNames = append(snapshotNames, name)
	}
	sort.Strings(snapshotNames)

	var lines []string
	lines = append(lines, mainWorkspaceName)
	for _, snapshotName := range snapshotNames {
		snapshot := m.snapshots[snapshotName]
		lines = append(lines, "`-- snapshot: "+snapshot.name+" (from "+snapshot.fromWorkspace+")")

		branchNames := make([]string, 0, len(m.workspaces))
		for name, workspace := range m.workspaces {
			if name == mainWorkspaceName || workspace.originSnapshot != snapshotName {
				continue
			}
			branchNames = append(branchNames, name)
		}
		sort.Strings(branchNames)
		for i, branchName := range branchNames {
			connector := "|-- "
			if i == len(branchNames)-1 {
				connector = "`-- "
			}
			lines = append(lines, "    "+connector+"workspace: "+branchName)
		}
	}
	return strings.Join(lines, "\n")
}

func (m *workspaceManager) newWorkspace(ctx context.Context, name string, source gbfs.FileSystem, originSnapshot string) (*managedWorkspace, error) {
	var factory gbfs.Factory
	if source == nil {
		factory = gbfs.Memory()
	} else {
		factory = gbfs.Overlay(gbfs.Snapshot(source))
	}

	runtime, err := gbash.New(
		gbash.WithFileSystem(gbash.CustomFileSystem(factory, m.workDir)),
		gbash.WithTracing(gbash.TraceConfig{Mode: gbash.TraceRedacted}),
	)
	if err != nil {
		return nil, fmt.Errorf("create runtime for %s: %w", name, err)
	}
	session, err := runtime.NewSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("create session for %s: %w", name, err)
	}
	return &managedWorkspace{
		name:           name,
		session:        session,
		originSnapshot: originSnapshot,
	}, nil
}

type treeNode struct {
	name     string
	fullPath string
	mode     stdfs.FileMode
	target   string
	children []*treeNode
}

func renderTree(ctx context.Context, fsys gbfs.FileSystem, root string) (string, error) {
	node, err := buildTree(ctx, fsys, root)
	if err != nil {
		return "", err
	}

	var lines []string
	lines = append(lines, node.fullPath)
	for i, child := range node.children {
		walkTreeLines(child, "", i == len(node.children)-1, &lines)
	}
	return strings.Join(lines, "\n"), nil
}

func buildTree(ctx context.Context, fsys gbfs.FileSystem, name string) (*treeNode, error) {
	info, err := fsys.Lstat(ctx, name)
	if err != nil {
		return nil, err
	}
	node := &treeNode{
		name:     path.Base(name),
		fullPath: gbfs.Clean(name),
		mode:     info.Mode(),
	}
	if info.Mode()&stdfs.ModeSymlink != 0 {
		target, err := fsys.Readlink(ctx, name)
		if err != nil {
			return nil, err
		}
		node.target = target
		return node, nil
	}
	if !info.IsDir() {
		return node, nil
	}

	entries, err := fsys.ReadDir(ctx, name)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		childPath := path.Join(gbfs.Clean(name), entry.Name())
		child, err := buildTree(ctx, fsys, childPath)
		if err != nil {
			return nil, err
		}
		node.children = append(node.children, child)
	}
	return node, nil
}

func walkTreeLines(node *treeNode, prefix string, last bool, lines *[]string) {
	connector := "|-- "
	childPrefix := prefix + "|   "
	if last {
		connector = "`-- "
		childPrefix = prefix + "    "
	}

	label := node.name
	switch {
	case node.mode.IsDir():
		label += "/"
	case node.mode&stdfs.ModeSymlink != 0:
		label += " -> " + node.target
	}
	*lines = append(*lines, prefix+connector+label)
	for i, child := range node.children {
		walkTreeLines(child, childPrefix, i == len(node.children)-1, lines)
	}
}

func renderSnippet(content string, maxLines int) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		lines = append(lines, "...")
	}

	var buf strings.Builder
	for i, line := range lines {
		_, _ = fmt.Fprintf(&buf, "%2d | %s\n", i+1, line)
	}
	return strings.TrimRight(buf.String(), "\n")
}

type fileState struct {
	mode   stdfs.FileMode
	data   []byte
	target string
}

func renderDiff(ctx context.Context, before, after gbfs.FileSystem, root string) (string, error) {
	beforeState, err := captureState(ctx, before, root)
	if err != nil {
		return "", err
	}
	afterState, err := captureState(ctx, after, root)
	if err != nil {
		return "", err
	}

	paths := make([]string, 0, len(beforeState)+len(afterState))
	seen := make(map[string]struct{}, len(beforeState)+len(afterState))
	for name := range beforeState {
		if name == root {
			continue
		}
		seen[name] = struct{}{}
		paths = append(paths, name)
	}
	for name := range afterState {
		if name == root {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		paths = append(paths, name)
	}
	sort.Strings(paths)

	var lines []string
	for _, name := range paths {
		beforeEntry, beforeOK := beforeState[name]
		afterEntry, afterOK := afterState[name]
		switch {
		case !beforeOK && afterOK:
			lines = append(lines, "+ "+formatPathForDiff(name, afterEntry.mode))
		case beforeOK && !afterOK:
			lines = append(lines, "- "+formatPathForDiff(name, beforeEntry.mode))
		case beforeOK && afterOK && !sameFileState(beforeEntry, afterEntry):
			lines = append(lines, "~ "+formatPathForDiff(name, afterEntry.mode))
		}
	}
	if len(lines) == 0 {
		return "(no changes)", nil
	}
	return strings.Join(lines, "\n"), nil
}

func captureState(ctx context.Context, fsys gbfs.FileSystem, name string) (map[string]fileState, error) {
	out := make(map[string]fileState)
	if err := walkState(ctx, fsys, gbfs.Clean(name), out); err != nil {
		return nil, err
	}
	return out, nil
}

func walkState(ctx context.Context, fsys gbfs.FileSystem, name string, out map[string]fileState) error {
	info, err := fsys.Lstat(ctx, name)
	if err != nil {
		return err
	}

	state := fileState{mode: info.Mode()}
	switch {
	case info.Mode()&stdfs.ModeSymlink != 0:
		target, err := fsys.Readlink(ctx, name)
		if err != nil {
			return err
		}
		state.target = target
	case info.IsDir():
	default:
		data, err := readSandboxFile(ctx, fsys, name)
		if err != nil {
			return err
		}
		state.data = []byte(data)
	}
	out[name] = state

	if !info.IsDir() {
		return nil
	}
	entries, err := fsys.ReadDir(ctx, name)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := walkState(ctx, fsys, path.Join(name, entry.Name()), out); err != nil {
			return err
		}
	}
	return nil
}

func sameFileState(left, right fileState) bool {
	return left.mode == right.mode && left.target == right.target && bytes.Equal(left.data, right.data)
}

func formatPathForDiff(name string, mode stdfs.FileMode) string {
	if mode.IsDir() {
		return name + "/"
	}
	return name
}

func renderMutationJournal(events []trace.Event) string {
	var lines []string
	for i := range events {
		event := &events[i]
		if event.Kind != trace.EventFileMutation || event.File == nil {
			continue
		}
		switch event.File.Action {
		case "copy", "rename":
			lines = append(lines, fmt.Sprintf("%s %s -> %s", event.File.Action, event.File.FromPath, event.File.ToPath))
		case "remove":
			lines = append(lines, fmt.Sprintf("remove %s", event.File.FromPath))
		default:
			lines = append(lines, fmt.Sprintf("%s %s", event.File.Action, event.File.Path))
		}
	}
	if len(lines) == 0 {
		return "(no file mutations recorded)"
	}
	return strings.Join(lines, "\n")
}

func countMutations(events []trace.Event) int {
	total := 0
	for i := range events {
		event := &events[i]
		if event.Kind == trace.EventFileMutation && event.File != nil {
			total++
		}
	}
	return total
}

func writeSandboxFile(ctx context.Context, fsys gbfs.FileSystem, name, content string, perm stdfs.FileMode) error {
	if err := fsys.MkdirAll(ctx, path.Dir(name), 0o755); err != nil {
		return err
	}
	file, err := fsys.OpenFile(ctx, name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = io.WriteString(file, content)
	return err
}

func readSandboxFile(ctx context.Context, fsys gbfs.FileSystem, name string) (string, error) {
	file, err := fsys.Open(ctx, name)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
