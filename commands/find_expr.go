package commands

import (
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

type findTrueExpr struct{}

type findCommandOptions struct {
	maxDepth    int
	hasMaxDepth bool
}

type findEvalContext struct {
	displayPath string
	name        string
	isDir       bool
	isEmpty     bool
	mtime       time.Time
	size        int64
}
