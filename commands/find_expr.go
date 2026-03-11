package commands

import (
	stdfs "io/fs"
	"regexp"
	"time"
)

type findCompare string

const (
	findCompareExact findCompare = "exact"
	findCompareMore  findCompare = "more"
	findCompareLess  findCompare = "less"
)

type findExpr interface{}

type findNameExpr struct {
	pattern    string
	ignoreCase bool
}

type findPathExpr struct {
	pattern    string
	ignoreCase bool
}

type findRegexExpr struct {
	regex *regexp.Regexp
}

type findTypeExpr struct {
	fileType byte
}

type findEmptyExpr struct{}

type findMTimeExpr struct {
	days       int
	comparison findCompare
}

type findNewerExpr struct {
	refPath        string
	resolvedTime   time.Time
	referenceReady bool
	referenceFound bool
}

type findSizeExpr struct {
	value      int64
	unit       byte
	comparison findCompare
}

type findPermMatch string

const (
	findPermExact findPermMatch = "exact"
	findPermAll   findPermMatch = "all"
	findPermAny   findPermMatch = "any"
)

type findPermExpr struct {
	mode      stdfs.FileMode
	matchType findPermMatch
}

type findPruneExpr struct{}

type findPrintExpr struct{}

type findNotExpr struct {
	expr findExpr
}

type findAndExpr struct {
	left  findExpr
	right findExpr
}

type findOrExpr struct {
	left  findExpr
	right findExpr
}

type findAction interface{}

type findExecAction struct {
	command   []string
	batchMode bool
}

type findPrintAction struct{}

type findPrint0Action struct{}

type findPrintfAction struct {
	format string
}

type findDeleteAction struct{}

type findCommandOptions struct {
	maxDepth    int
	hasMaxDepth bool
	minDepth    int
	hasMinDepth bool
	depthFirst  bool
}

type findEvalContext struct {
	displayPath string
	name        string
	isDir       bool
	isEmpty     bool
	mtime       time.Time
	size        int64
	mode        stdfs.FileMode
}

type findEvalResult struct {
	matches bool
	pruned  bool
	printed bool
}

type findPrintData struct {
	path          string
	name          string
	size          int64
	mtime         time.Time
	mode          stdfs.FileMode
	isDirectory   bool
	depth         int
	startingPoint string
}
