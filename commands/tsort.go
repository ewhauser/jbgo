package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"sort"
	"strings"
)

type Tsort struct{}

func NewTsort() *Tsort {
	return &Tsort{}
}

func (c *Tsort) Name() string {
	return "tsort"
}

func (c *Tsort) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Tsort) Spec() CommandSpec {
	return CommandSpec{
		Name: "tsort",
		About: "Topological sort the strings in FILE.\n" +
			"Strings are defined as any sequence of tokens separated by whitespace (tab, space, or newline), ordering them based on dependencies in a directed acyclic graph (DAG).\n" +
			"Useful for scheduling and determining execution order.\n" +
			"If FILE is not passed in, stdin is used instead.",
		Usage: "tsort [OPTION]... [FILE]",
		Options: []OptionSpec{
			{Name: "warn", Short: 'w', Hidden: true},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Default: []string{"-"}},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
			AutoHelp:              true,
			AutoVersion:           true,
		},
	}
}

func (c *Tsort) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	graph := newTsortGraph(matches.Arg("file"))
	if err := tsortProcessInput(ctx, inv, graph.name, graph); err != nil {
		return err
	}
	return graph.run(ctx, inv)
}

type tsortNode struct {
	successors       []string
	predecessorCount int
}

type tsortGraph struct {
	name  string
	nodes map[string]*tsortNode
}

func newTsortGraph(name string) *tsortGraph {
	return &tsortGraph{
		name:  name,
		nodes: make(map[string]*tsortNode),
	}
}

func (g *tsortGraph) ensureNode(name string) *tsortNode {
	node := g.nodes[name]
	if node == nil {
		node = &tsortNode{}
		g.nodes[name] = node
	}
	return node
}

func (g *tsortGraph) addEdge(from, to string) {
	fromNode := g.ensureNode(from)
	if from == to {
		return
	}
	fromNode.successors = append(fromNode.successors, to)
	toNode := g.ensureNode(to)
	toNode.predecessorCount++
}

func (g *tsortGraph) removeEdge(from, to string) {
	node := g.nodes[from]
	if node == nil {
		return
	}
	for i, successor := range node.successors {
		if successor != to {
			continue
		}
		node.successors = append(node.successors[:i], node.successors[i+1:]...)
		break
	}
	if successorNode := g.nodes[to]; successorNode != nil {
		successorNode.predecessorCount--
	}
}

func (g *tsortGraph) initialFrontier() []string {
	frontier := make([]string, 0, len(g.nodes))
	for name, node := range g.nodes {
		if node.predecessorCount == 0 {
			frontier = append(frontier, name)
		}
	}
	sort.Strings(frontier)
	return frontier
}

func (g *tsortGraph) run(ctx context.Context, inv *Invocation) error {
	frontier := g.initialFrontier()
	writer := bufio.NewWriter(inv.Stdout)
	hadLoop := false

	for len(g.nodes) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}

		for len(frontier) == 0 {
			cycle := g.detectCycle()
			if len(cycle) == 0 {
				return &ExitError{Code: 1}
			}
			if err := tsortShowLoop(inv, g.name, cycle); err != nil {
				return err
			}
			hadLoop = true
			from := cycle[len(cycle)-1]
			to := cycle[0]
			g.removeEdge(from, to)
			if node := g.nodes[to]; node != nil && node.predecessorCount == 0 {
				frontier = append(frontier, to)
			}
		}

		current := frontier[0]
		frontier = frontier[1:]
		if _, err := writer.WriteString(current); err != nil {
			if tsortBrokenPipe(err) {
				return nil
			}
			return &ExitError{Code: 1, Err: err}
		}
		if err := writer.WriteByte('\n'); err != nil {
			if tsortBrokenPipe(err) {
				return nil
			}
			return &ExitError{Code: 1, Err: err}
		}

		node := g.nodes[current]
		delete(g.nodes, current)
		for i := len(node.successors) - 1; i >= 0; i-- {
			successor := node.successors[i]
			successorNode := g.nodes[successor]
			if successorNode == nil {
				continue
			}
			successorNode.predecessorCount--
			if successorNode.predecessorCount == 0 {
				frontier = append(frontier, successor)
			}
		}
	}

	if err := writer.Flush(); err != nil {
		if tsortBrokenPipe(err) {
			return nil
		}
		return &ExitError{Code: 1, Err: err}
	}
	if hadLoop {
		return &ExitError{Code: 1}
	}
	return nil
}

type tsortVisitedState int

const (
	tsortVisitedOpen tsortVisitedState = iota + 1
	tsortVisitedClosed
)

type tsortCycleFrame struct {
	node string
	next int
}

func (g *tsortGraph) detectCycle() []string {
	nodes := make([]string, 0, len(g.nodes))
	for name := range g.nodes {
		nodes = append(nodes, name)
	}
	sort.Strings(nodes)

	visited := make(map[string]tsortVisitedState, len(g.nodes))
	for _, start := range nodes {
		if visited[start] != 0 {
			continue
		}
		visited[start] = tsortVisitedOpen
		stack := []tsortCycleFrame{{node: start}}
		path := []string{start}
		pathIndex := map[string]int{start: 0}

		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			successors := g.nodes[top.node].successors
			if top.next >= len(successors) {
				visited[top.node] = tsortVisitedClosed
				delete(pathIndex, top.node)
				path = path[:len(path)-1]
				stack = stack[:len(stack)-1]
				continue
			}

			next := successors[top.next]
			top.next++
			if g.nodes[next] == nil {
				continue
			}
			switch visited[next] {
			case tsortVisitedClosed:
				continue
			case tsortVisitedOpen:
				return append([]string(nil), path[pathIndex[next]:]...)
			default:
				visited[next] = tsortVisitedOpen
				pathIndex[next] = len(path)
				path = append(path, next)
				stack = append(stack, tsortCycleFrame{node: next})
			}
		}
	}

	return nil
}

func tsortProcessInput(ctx context.Context, inv *Invocation, name string, graph *tsortGraph) error {
	if name == "-" {
		return tsortReadGraph(ctx, inv, name, inv.Stdin, graph)
	}

	info, _, err := statPath(ctx, inv, name)
	if err != nil {
		return tsortOpenError(inv, name, err)
	}
	if info.IsDir() {
		return tsortDirectoryError(inv, name)
	}

	file, _, err := openRead(ctx, inv, name)
	if err != nil {
		return tsortOpenError(inv, name, err)
	}
	defer func() { _ = file.Close() }()

	return tsortReadGraph(ctx, inv, name, file, graph)
}

func tsortReadGraph(ctx context.Context, inv *Invocation, name string, reader io.Reader, graph *tsortGraph) error {
	buffered := bufio.NewReader(reader)
	var pending string
	hasPending := false

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		line, err := buffered.ReadString('\n')
		for token := range strings.FieldsSeq(line) {
			if hasPending {
				graph.addEdge(pending, token)
				hasPending = false
				continue
			}
			pending = token
			hasPending = true
		}

		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		return tsortReadError(inv, name, err)
	}

	if hasPending {
		return exitf(inv, 1, "tsort: %s: input contains an odd number of tokens", tsortDisplayName(name))
	}
	return nil
}

func tsortShowLoop(inv *Invocation, name string, cycle []string) error {
	if _, err := fmt.Fprintf(inv.Stderr, "tsort: %s: input contains a loop:\n", tsortDisplayName(name)); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	for _, node := range cycle {
		if _, err := fmt.Fprintf(inv.Stderr, "tsort: %s\n", node); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func tsortOpenError(inv *Invocation, name string, err error) error {
	if errors.Is(err, stdfs.ErrNotExist) {
		return exitf(inv, 1, "tsort: %s: No such file or directory", tsortDisplayName(name))
	}
	return exitf(inv, exitCodeForError(err), "tsort: %s: %v", tsortDisplayName(name), err)
}

func tsortReadError(inv *Invocation, name string, err error) error {
	return exitf(inv, exitCodeForError(err), "tsort: %s: read error: %v", tsortDisplayName(name), err)
}

func tsortDirectoryError(inv *Invocation, name string) error {
	return exitf(inv, 1, "tsort: %s: read error: Is a directory", tsortDisplayName(name))
}

func tsortDisplayName(name string) string {
	if strings.ContainsAny(name, " \t\r\n") {
		return quoteGNUOperand(name)
	}
	return name
}

func tsortBrokenPipe(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || strings.Contains(strings.ToLower(err.Error()), "broken pipe")
}

var _ Command = (*Tsort)(nil)
var _ SpecProvider = (*Tsort)(nil)
var _ ParsedRunner = (*Tsort)(nil)
