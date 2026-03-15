package searchadapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"path"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode/utf8"

	gbfs "github.com/ewhauser/gbash/fs"
)

// Query describes a grep/rg-like search request that may use an indexed
// provider when the caller marks it as eligible.
type Query struct {
	Roots         []string
	Literal       string
	IgnoreCase    bool
	IncludeGlobs  []string
	ExcludeGlobs  []string
	Limit         int
	WantOffsets   bool
	IndexEligible bool
}

// VerifyFunc lets callers verify or reject indexed candidates with userspace
// semantics.
type VerifyFunc func(context.Context, gbfs.SearchHit) (bool, error)

// Result reports the resolved candidates and whether the indexed path was used.
type Result struct {
	Hits      []gbfs.SearchHit
	UsedIndex bool
	Truncated bool
}

const MaxPrefilterLiterals = 8

// PrefilterResult reports candidate paths selected by indexed literal queries.
type PrefilterResult struct {
	CandidatePaths map[string]struct{}
	Literals       []string
	UsedIndex      bool
}

// Search resolves indexed candidates when every requested root exposes a fresh
// provider and the caller marks the query as index-eligible. Otherwise it falls
// back to direct filesystem scanning.
func Search(ctx context.Context, fsys gbfs.FileSystem, query *Query, verify VerifyFunc) (Result, error) {
	if query == nil {
		return Result{}, fmt.Errorf("searchadapter: query is required")
	}
	roots := normalizedRoots(fsys, query.Roots)
	if query.Literal == "" {
		return Result{}, fmt.Errorf("searchadapter: literal is required")
	}
	if !query.IndexEligible {
		return scan(ctx, fsys, roots, query, verify)
	}

	capable, ok := fsys.(gbfs.SearchCapable)
	if !ok {
		return scan(ctx, fsys, roots, query, verify)
	}

	hits := make([]gbfs.SearchHit, 0)
	truncated := false
	for _, root := range roots {
		provider, ok := capable.SearchProviderForPath(root)
		if !ok {
			return scan(ctx, fsys, roots, query, verify)
		}
		if !providerSupportsQuery(provider.SearchCapabilities(), root, query) {
			return scan(ctx, fsys, roots, query, verify)
		}
		status := provider.IndexStatus()
		if status.CurrentGeneration != status.IndexedGeneration {
			return scan(ctx, fsys, roots, query, verify)
		}

		limit := remainingLimit(query.Limit, len(hits))
		if verify != nil {
			limit = 0
		}
		result, err := provider.Search(ctx, &gbfs.SearchQuery{
			Root:         root,
			Literal:      query.Literal,
			IgnoreCase:   query.IgnoreCase,
			IncludeGlobs: query.IncludeGlobs,
			ExcludeGlobs: query.ExcludeGlobs,
			Limit:        limit,
			WantOffsets:  query.WantOffsets,
		})
		if err != nil {
			if errors.Is(err, gbfs.ErrSearchUnsupported) {
				return scan(ctx, fsys, roots, query, verify)
			}
			return Result{}, err
		}
		if result.Status.CurrentGeneration != result.Status.IndexedGeneration {
			return scan(ctx, fsys, roots, query, verify)
		}

		for _, hit := range result.Hits {
			if verify != nil {
				ok, err := verify(ctx, hit)
				if err != nil {
					return Result{}, err
				}
				if !ok {
					continue
				}
			}
			hits = append(hits, hit)
			if query.Limit > 0 && len(hits) >= query.Limit {
				return Result{
					Hits:      hits,
					UsedIndex: true,
					Truncated: true,
				}, nil
			}
		}
		truncated = truncated || result.Truncated
	}

	return Result{
		Hits:      hits,
		UsedIndex: true,
		Truncated: truncated,
	}, nil
}

// RegexpPrefilterLiterals extracts a bounded set of literals such that every
// real regexp match must contain at least one returned literal.
func RegexpPrefilterLiterals(re *regexp.Regexp) ([]string, bool) {
	if re == nil {
		return nil, false
	}

	literals, ok := extractRegexpPrefilterLiterals(re.String())
	if !ok || len(literals) == 0 {
		prefix, _ := re.LiteralPrefix()
		if prefix == "" {
			return nil, false
		}
		literals = []string{prefix}
	}

	literals = normalizePrefilterLiterals(literals)
	if len(literals) == 0 || len(literals) > MaxPrefilterLiterals {
		return nil, false
	}
	return literals, true
}

// PrefilterCandidates uses a fresh indexed provider to build a candidate set
// for eligiblePaths under root. It falls back cleanly when indexing is
// unsupported, stale, or missing required capabilities.
func PrefilterCandidates(ctx context.Context, provider gbfs.SearchProvider, root string, eligiblePaths []string, re *regexp.Regexp, ignoreCase bool) (PrefilterResult, error) {
	if provider == nil {
		return PrefilterResult{}, nil
	}

	literals, ok := RegexpPrefilterLiterals(re)
	if !ok {
		return PrefilterResult{}, nil
	}
	ignoreCase = ignoreCase || regexpHasFoldCase(re)
	caps := provider.SearchCapabilities()
	if !caps.LiteralSearch || !caps.RootRestriction {
		return PrefilterResult{}, nil
	}
	if ignoreCase && !caps.IgnoreCaseLiteralSearch {
		return PrefilterResult{}, nil
	}
	status := provider.IndexStatus()
	if status.CurrentGeneration != status.IndexedGeneration {
		return PrefilterResult{}, nil
	}

	eligible := make(map[string]struct{}, len(eligiblePaths))
	for _, name := range eligiblePaths {
		eligible[gbfs.Clean(name)] = struct{}{}
	}
	if len(eligible) == 0 {
		return PrefilterResult{
			CandidatePaths: make(map[string]struct{}),
			Literals:       literals,
			UsedIndex:      true,
		}, nil
	}

	candidates := make(map[string]struct{})
	for _, literal := range literals {
		result, err := provider.Search(ctx, &gbfs.SearchQuery{
			Root:       root,
			Literal:    literal,
			IgnoreCase: ignoreCase,
		})
		if err != nil {
			if errors.Is(err, gbfs.ErrSearchUnsupported) {
				return PrefilterResult{}, nil
			}
			return PrefilterResult{}, err
		}
		if result.Status.CurrentGeneration != result.Status.IndexedGeneration {
			return PrefilterResult{}, nil
		}
		for _, hit := range result.Hits {
			pathValue := gbfs.Clean(hit.Path)
			if _, ok := eligible[pathValue]; ok {
				candidates[pathValue] = struct{}{}
			}
		}
	}

	return PrefilterResult{
		CandidatePaths: candidates,
		Literals:       literals,
		UsedIndex:      true,
	}, nil
}

func scan(ctx context.Context, fsys gbfs.FileSystem, roots []string, query *Query, verify VerifyFunc) (Result, error) {
	hits := make([]gbfs.SearchHit, 0)
	truncated := false
	for _, root := range roots {
		if err := scanPath(ctx, fsys, gbfs.Clean(root), gbfs.Clean(root), query, verify, &hits, &truncated); err != nil {
			return Result{}, err
		}
		if truncated {
			break
		}
	}
	return Result{
		Hits:      hits,
		UsedIndex: false,
		Truncated: truncated,
	}, nil
}

func scanPath(ctx context.Context, fsys gbfs.FileSystem, currentRoot, current string, query *Query, verify VerifyFunc, hits *[]gbfs.SearchHit, truncated *bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if *truncated {
		return nil
	}

	linfo, err := fsys.Lstat(ctx, current)
	if err != nil {
		return err
	}
	if linfo.Mode()&stdfs.ModeSymlink != 0 {
		info, err := fsys.Stat(ctx, current)
		if err != nil || !scanIndexableFileInfo(info) {
			return nil
		}
		return scanFile(ctx, fsys, currentRoot, current, query, verify, hits, truncated)
	}
	if !linfo.IsDir() {
		if !scanIndexableFileInfo(linfo) {
			return nil
		}
		return scanFile(ctx, fsys, currentRoot, current, query, verify, hits, truncated)
	}

	entries, err := fsys.ReadDir(ctx, current)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	for _, name := range names {
		if err := scanPath(ctx, fsys, currentRoot, joinChild(current, name), query, verify, hits, truncated); err != nil {
			return err
		}
		if *truncated {
			return nil
		}
	}
	return nil
}

func scanIndexableFileInfo(info stdfs.FileInfo) bool {
	return info != nil && info.Mode().IsRegular()
}

func scanFile(ctx context.Context, fsys gbfs.FileSystem, root, name string, query *Query, verify VerifyFunc, hits *[]gbfs.SearchHit, truncated *bool) error {
	if !pathMatchesGlobs(root, name, query.IncludeGlobs, query.ExcludeGlobs) {
		return nil
	}
	data, err := readAll(ctx, fsys, name)
	if err != nil {
		return err
	}

	matched := false
	var offsets []int64
	if query.IgnoreCase {
		matched, offsets = containsIgnoreCase(data, []byte(query.Literal), query.WantOffsets)
	} else {
		matched = bytes.Contains(data, []byte(query.Literal))
		if query.WantOffsets {
			offsets = literalOffsets(data, []byte(query.Literal))
			matched = len(offsets) > 0
		}
	}
	if !matched {
		return nil
	}

	hit := gbfs.SearchHit{
		Path:     name,
		Offsets:  offsets,
		Verified: true,
	}
	if verify != nil {
		ok, err := verify(ctx, hit)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}

	*hits = append(*hits, hit)
	if query.Limit > 0 && len(*hits) >= query.Limit {
		*truncated = true
	}
	return nil
}

func normalizedRoots(fsys gbfs.FileSystem, roots []string) []string {
	if len(roots) == 0 {
		return []string{"/"}
	}
	cwd := "/"
	if fsys != nil {
		cwd = fsys.Getwd()
	}
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		out = append(out, gbfs.Resolve(cwd, root))
	}
	return out
}

func extractRegexpPrefilterLiterals(expr string) ([]string, bool) {
	parsed, err := syntax.Parse(expr, syntax.Perl)
	if err != nil {
		return nil, false
	}
	parsed = parsed.Simplify()
	literals := mandatoryLiteralSet(parsed)
	if len(literals) == 0 {
		return nil, false
	}
	return literals, true
}

func regexpHasFoldCase(re *regexp.Regexp) bool {
	if re == nil {
		return false
	}
	parsed, err := syntax.Parse(re.String(), syntax.Perl)
	if err != nil {
		return false
	}
	return regexpTreeHasFoldCase(parsed)
}

func regexpTreeHasFoldCase(re *syntax.Regexp) bool {
	if re == nil {
		return false
	}
	if re.Flags&syntax.FoldCase != 0 {
		return true
	}
	for _, sub := range re.Sub {
		if regexpTreeHasFoldCase(sub) {
			return true
		}
	}
	return false
}

func mandatoryLiteralSet(re *syntax.Regexp) []string {
	if re == nil {
		return nil
	}
	switch re.Op {
	case syntax.OpLiteral:
		if len(re.Rune) == 0 {
			return nil
		}
		return []string{string(re.Rune)}
	case syntax.OpCapture:
		if len(re.Sub) == 0 {
			return nil
		}
		return mandatoryLiteralSet(re.Sub[0])
	case syntax.OpPlus:
		if len(re.Sub) == 0 {
			return nil
		}
		return mandatoryLiteralSet(re.Sub[0])
	case syntax.OpRepeat:
		if re.Min <= 0 || len(re.Sub) == 0 {
			return nil
		}
		return mandatoryLiteralSet(re.Sub[0])
	case syntax.OpConcat:
		var best []string
		for _, sub := range re.Sub {
			candidate := mandatoryLiteralSet(sub)
			if betterMandatoryLiteralSet(candidate, best) {
				best = candidate
			}
		}
		return best
	case syntax.OpAlternate:
		out := make([]string, 0, len(re.Sub))
		for _, sub := range re.Sub {
			candidate := mandatoryLiteralSet(sub)
			if len(candidate) == 0 {
				return nil
			}
			out = append(out, candidate...)
		}
		return out
	default:
		return nil
	}
}

func betterMandatoryLiteralSet(candidate, current []string) bool {
	if len(candidate) == 0 {
		return false
	}
	if len(current) == 0 {
		return true
	}
	candidateLen := len(bestMandatoryLiteral(candidate))
	currentLen := len(bestMandatoryLiteral(current))
	if candidateLen != currentLen {
		return candidateLen > currentLen
	}
	if len(candidate) != len(current) {
		return len(candidate) < len(current)
	}
	return strings.Join(candidate, "\x00") < strings.Join(current, "\x00")
}

func bestMandatoryLiteral(literals []string) string {
	best := ""
	for _, literal := range literals {
		if len(literal) > len(best) || (len(literal) == len(best) && literal < best) {
			best = literal
		}
	}
	return best
}

func normalizePrefilterLiterals(literals []string) []string {
	if len(literals) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(literals))
	out := make([]string, 0, len(literals))
	for _, literal := range literals {
		if literal == "" {
			continue
		}
		if _, ok := seen[literal]; ok {
			continue
		}
		seen[literal] = struct{}{}
		out = append(out, literal)
	}
	slices.SortFunc(out, func(a, b string) int {
		switch {
		case len(a) != len(b):
			return len(b) - len(a)
		case a < b:
			return -1
		case a > b:
			return 1
		default:
			return 0
		}
	})
	return out
}

func providerSupportsQuery(caps gbfs.SearchCapabilities, root string, query *Query) bool {
	if query == nil || !caps.LiteralSearch {
		return false
	}
	if query.IgnoreCase && !caps.IgnoreCaseLiteralSearch {
		return false
	}
	if len(query.IncludeGlobs) > 0 && !caps.IncludeGlobs {
		return false
	}
	if len(query.ExcludeGlobs) > 0 && !caps.ExcludeGlobs {
		return false
	}
	if query.WantOffsets && !caps.Offsets {
		return false
	}
	if gbfs.Clean(root) != "/" && !caps.RootRestriction {
		return false
	}
	return true
}

func remainingLimit(limit, used int) int {
	if limit <= 0 {
		return 0
	}
	remaining := limit - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

func joinChild(parent, child string) string {
	if parent == "/" {
		return "/" + child
	}
	return parent + "/" + child
}

func readAll(ctx context.Context, fsys gbfs.FileSystem, name string) ([]byte, error) {
	file, err := fsys.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

func containsIgnoreCase(data, literal []byte, wantOffsets bool) (matched bool, offsets []int64) {
	useASCIIFolding := !utf8.Valid(data) || !utf8.Valid(literal) || isASCII(data)
	foldedLiteral := foldASCII(literal)
	if useASCIIFolding {
		folded := foldASCII(data)
		if !wantOffsets {
			return bytes.Contains(folded, foldedLiteral), nil
		}
		offsets = literalOffsets(folded, foldedLiteral)
		return len(offsets) > 0, offsets
	}
	if !wantOffsets {
		return len(equalFoldOffsets(data, string(literal))) > 0, nil
	}
	offsets = equalFoldOffsets(data, string(literal))
	return len(offsets) > 0, offsets
}

func literalOffsets(data, literal []byte) []int64 {
	if len(literal) == 0 {
		return nil
	}
	offsets := make([]int64, 0, 1)
	for start := 0; start <= len(data)-len(literal); {
		idx := bytes.Index(data[start:], literal)
		if idx < 0 {
			break
		}
		abs := start + idx
		offsets = append(offsets, int64(abs))
		start = abs + 1
	}
	return offsets
}

func equalFoldOffsets(data []byte, literal string) []int64 {
	if literal == "" {
		return nil
	}
	runeCount := utf8.RuneCountInString(literal)
	offsets := make([]int64, 0, 1)
	for i := 0; i < len(data); {
		end := advanceRunes(data, i, runeCount)
		if end >= 0 && strings.EqualFold(string(data[i:end]), literal) {
			offsets = append(offsets, int64(i))
		}
		_, size := utf8.DecodeRune(data[i:])
		if size <= 0 {
			break
		}
		i += size
	}
	return offsets
}

func advanceRunes(data []byte, start, count int) int {
	pos := start
	for range count {
		if pos >= len(data) {
			return -1
		}
		_, size := utf8.DecodeRune(data[pos:])
		if size <= 0 {
			return -1
		}
		pos += size
	}
	return pos
}

func foldASCII(data []byte) []byte {
	out := make([]byte, len(data))
	copy(out, data)
	for i := range out {
		if out[i] >= 'A' && out[i] <= 'Z' {
			out[i] += 'a' - 'A'
		}
	}
	return out
}

func isASCII(data []byte) bool {
	for _, b := range data {
		if b >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func pathMatchesGlobs(root, pathValue string, include, exclude []string) bool {
	relative := strings.TrimPrefix(gbfs.Clean(pathValue), gbfs.Clean(root))
	relative = strings.TrimPrefix(relative, "/")
	base := path.Base(pathValue)

	if len(include) > 0 {
		allowed := false
		for _, glob := range include {
			if globMatches(glob, relative, base) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	for _, glob := range exclude {
		if globMatches(glob, relative, base) {
			return false
		}
	}
	return true
}

func globMatches(glob, relative, base string) bool {
	pattern := strings.TrimSpace(glob)
	if pattern == "" {
		return false
	}
	targets := []string{relative}
	if !strings.Contains(pattern, "/") {
		targets = append(targets, base)
	}
	if rooted, ok := strings.CutPrefix(pattern, "/"); ok {
		pattern = rooted
		targets = []string{relative}
	}
	re, err := globRegexp(pattern)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(targets, re.MatchString)
}

func globRegexp(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				if i+2 < len(glob) && glob[i+2] == '/' {
					b.WriteString(`(?:.*/)?`)
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		case '[':
			j := i + 1
			if j < len(glob) && glob[j] == '!' {
				j++
			}
			if j < len(glob) && glob[j] == ']' {
				j++
			}
			for j < len(glob) && glob[j] != ']' {
				j++
			}
			if j >= len(glob) {
				b.WriteString(`\[`)
				continue
			}
			class := glob[i : j+1]
			if strings.HasPrefix(class, "[!") {
				class = "[^" + class[2:]
			}
			b.WriteString(class)
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(glob[i])))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
