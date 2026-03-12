package runtime

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"testing"
)

func FuzzFilePathCommands(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		name string
		data []byte
	}{
		{"alpha", []byte("hello\n")},
		{"notes-1", []byte("# title\nbody\n")},
		{"data.bin", []byte{0x00, 0x01, 0x02, 0x03, 0xff}},
	}
	for _, seed := range seeds {
		f.Add(seed.name, seed.data)
	}

	f.Fuzz(func(t *testing.T, rawName string, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := fuzzPath(rawName) + ".txt"
		copyPath := fuzzPath(rawName) + ".copy"
		movedPath := fuzzPath(rawName) + ".moved"
		linkPath := fuzzPath(rawName) + ".ln"
		data := clampFuzzData(rawData)

		writeSessionFile(t, session, inputPath, data)

		script := fmt.Appendf(nil,
			"touch %s\ncp -pv %s %s\nmv -v %s %s\nln -s -f %s %s\nreadlink %s >/tmp/readlink.out\nstat %s >/tmp/stat.out\nbasename --suffix=.moved %s >/tmp/base.out\ndirname %s >/tmp/dir.out\nchmod 600 %s\nmkdir -p /tmp/fuzz-empty/sub\nrmdir /tmp/fuzz-empty/sub\nfile --brief %s >/tmp/file.out\nrm %s %s %s\n",
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(copyPath),
			shellQuote(copyPath),
			shellQuote(movedPath),
			shellQuote(movedPath),
			shellQuote(linkPath),
			shellQuote(linkPath),
			shellQuote(movedPath),
			shellQuote(movedPath),
			shellQuote(movedPath),
			shellQuote(movedPath),
			shellQuote(movedPath),
			shellQuote(linkPath),
			shellQuote(movedPath),
			shellQuote(inputPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzDirectoryTraversalCommands(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		name string
		data []byte
	}{
		{"alpha", []byte("hello\n")},
		{"notes-1", []byte("# title\nbody\n")},
		{"data.bin", []byte{0x00, 0x01, 0x02, 0x03, 0xff}},
	}
	for _, seed := range seeds {
		f.Add(seed.name, seed.data)
	}

	f.Fuzz(func(t *testing.T, rawName string, rawData []byte) {
		session := newFuzzSession(t, rt)
		inputPath := fuzzPath(rawName) + ".txt"
		treeDir := path.Join("/tmp", "fuzz-tree")
		treeFile := path.Join(treeDir, "sub", "item.txt")
		treeLink := path.Join(treeDir, "item.ln")
		data := clampFuzzData(rawData)

		writeSessionFile(t, session, inputPath, data)

		script := fmt.Appendf(nil,
			"mkdir -p %s\ncp %s %s\nln -s -f %s %s\ndu -a %s >/tmp/du.out\ntree -a -L 2 %s >/tmp/tree.out\nrm -r -f %s\n",
			shellQuote(path.Dir(treeFile)),
			shellQuote(inputPath),
			shellQuote(treeFile),
			shellQuote(treeFile),
			shellQuote(treeLink),
			shellQuote(treeDir),
			shellQuote(treeDir),
			shellQuote(treeDir),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzTextSearchCommands(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		data   []byte
		needle string
	}{
		{[]byte("alpha\nbeta\nbeta\ngamma\n"), "beta"},
		{[]byte("one,two,three\nfour,five,six\n"), "two"},
		{[]byte("abc123\nxyz789\n"), "123"},
	}
	for _, seed := range seeds {
		f.Add(seed.data, seed.needle)
	}

	f.Fuzz(func(t *testing.T, rawData []byte, rawNeedle string) {
		session := newFuzzSession(t, rt)
		text := normalizeFuzzText(rawData)
		needle := sanitizeFuzzToken(rawNeedle)
		inputPath := "/tmp/input.txt"
		otherPath := "/tmp/input2.txt"
		joinLeftPath, joinRightPath := writeJoinFixtures(t, session, text)

		writeSessionFile(t, session, inputPath, text)
		writeSessionFile(t, session, otherPath, []byte(strings.ToUpper(string(text))))

		script := fmt.Appendf(nil,
			"printf '1,3p\\n' >/tmp/sed.fuzz\nsort %s >/tmp/sort.txt || true\nuniq --ignore-case /tmp/sort.txt >/tmp/uniq.txt || true\ncut --only-delimited -c 1-8 %s >/tmp/cut.txt || true\nsed -f /tmp/sed.fuzz %s >/tmp/sed.txt || true\ngrep -n %s %s >/tmp/grep.txt || true\nrg -n %s /tmp >/tmp/rg.txt || true\nawk '{print NF}' %s >/tmp/awk.txt || true\nhead --bytes=3 %s >/tmp/head.txt || true\ntail --bytes=3 %s >/tmp/tail.txt || true\nwc %s >/tmp/wc.txt\ntr --delete '[:digit:]' < %s >/tmp/tr.txt || true\nrev %s >/tmp/rev.txt || true\nnl -ba -n rz %s >/tmp/nl.txt || true\ntac %s >/tmp/tac.txt || true\nsplit -n 3 --additional-suffix=.part %s /tmp/split- || true\ncat /tmp/split-aa.part >/tmp/split.txt || true\npaste --serial --delimiters=, %s >/tmp/paste.txt || true\ncomm -1 /tmp/sort.txt /tmp/sort.txt >/tmp/comm.txt || true\njoin %s %s >/tmp/join.txt || true\ndiff -u %s %s >/tmp/diff.txt || true\nbase64 --wrap=0 %s | base64 -d >/tmp/base64.txt || true\ncat --number %s >/tmp/cat.txt || true\n",
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(needle),
			shellQuote(inputPath),
			shellQuote(needle),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(joinLeftPath),
			shellQuote(joinRightPath),
			shellQuote(inputPath),
			shellQuote(otherPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzShellProcessCommands(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		data  []byte
		value string
	}{
		{[]byte("alpha beta\n"), "VALUE"},
		{[]byte("one\ntwo\nthree\n"), "nested-value"},
		{[]byte("x y z\n"), "with spaces"},
	}
	for _, seed := range seeds {
		f.Add(seed.data, seed.value)
	}

	f.Fuzz(func(t *testing.T, rawData []byte, rawValue string) {
		session := newFuzzSession(t, rt)
		text := normalizeFuzzText(rawData)
		value := sanitizeFuzzToken(rawValue)
		inputPath := "/tmp/stdin.txt"

		writeSessionFile(t, session, inputPath, text)

		script := fmt.Appendf(nil,
			"cat %s | tee /tmp/tee.txt >/tmp/tee.out\nenv --ignore-environment ONLY=%s printenv ONLY >/tmp/env.txt\nprintenv HOME >/tmp/printenv.txt\nwhich echo >/tmp/which.txt\nhelp -s pwd >/tmp/help.txt\ndate -u -d 2024-01-02T03:04:05 +%%F >/tmp/date.txt\ndate --utc --date 2024-01-02T03:04:05 +%%Z >/tmp/date-utc.txt\ndate --date 2024-01-02T03:04:05 --iso-8601 >/tmp/date-iso.txt\ndate --date 2024-01-02T03:04:05 --rfc-email >/tmp/date-rfc.txt\nsleep 0.001\ntrue\n/bin/false || true\n",
			shellQuote(inputPath),
			shellQuote(value),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzNestedShellCommands(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		data  []byte
		value string
	}{
		{[]byte("alpha beta\n"), "VALUE"},
		{[]byte("one\ntwo\nthree\n"), "nested-value"},
		{[]byte("x y z\n"), "with spaces"},
	}
	for _, seed := range seeds {
		f.Add(seed.data, seed.value)
	}

	f.Fuzz(func(t *testing.T, rawData []byte, rawValue string) {
		session := newFuzzSession(t, rt)
		text := normalizeFuzzText(rawData)
		value := sanitizeFuzzToken(rawValue)
		inputPath := "/tmp/stdin.txt"

		writeSessionFile(t, session, inputPath, text)

		script := fmt.Appendf(nil,
			"timeout --signal TERM --kill-after 0.01 0.01 sleep 1 || true\nprintf 'echo from-stdin\\n' | sh >/tmp/sh.txt\nbash -c 'echo \"$1\"' ignored %s >/tmp/bash.txt\ncat %s | xargs --verbose --max-args 1 echo >/tmp/xargs.txt || true\n",
			shellQuote(value),
			shellQuote(inputPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzDataCommands(f *testing.F) {
	rt := newFuzzRuntime(f)
	addStructuredDataSeeds(f)

	f.Fuzz(func(t *testing.T, rawValue string, rawJSON []byte) {
		session := newFuzzSession(t, rt)
		value := prepareStructuredDataFixtures(t, session, rawValue, rawJSON)

		script := fmt.Appendf(nil,
			"jq -r '.value' /tmp/input.json >/tmp/jq-value.txt\njq -c '.items' /tmp/input.json >/tmp/jq-items.txt\njq -n --arg value %s '{value:$value}' >/tmp/jq-build.txt\njq '.value' /tmp/raw.json >/tmp/jq-raw.txt || true\nbase64 /tmp/input.json | base64 -d >/tmp/base64-json.txt || true\nsha256sum /tmp/input.json >/tmp/sha256-file.txt\ncat /tmp/input.json | sha256sum >/tmp/sha256-stdin.txt\nsha256sum /tmp/input.json >/tmp/checksums.txt\nsha256sum -c /tmp/checksums.txt >/tmp/sha256-check.txt\n",
			shellQuote(value),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzYQCommands(f *testing.F) {
	rt := newFuzzRuntime(f)
	addStructuredDataSeeds(f)

	f.Fuzz(func(t *testing.T, rawValue string, rawJSON []byte) {
		session := newFuzzSession(t, rt)
		_ = prepareStructuredDataFixtures(t, session, rawValue, rawJSON)

		script := []byte(
			"yq -p yaml -o yaml '.value' /tmp/input.yaml >/tmp/yq-value.txt\n" +
				"yq -p json -o json '.items' /tmp/input.json >/tmp/yq-items.txt\n" +
				"yq -n '.value = \"built\"' >/tmp/yq-build.txt\n",
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzSQLiteCommands(f *testing.F) {
	rt := newFuzzRuntime(f)
	addStructuredDataSeeds(f)
	warmFuzzSQLite(f, rt)

	f.Fuzz(func(t *testing.T, rawValue string, rawJSON []byte) {
		session := newFuzzSession(t, rt)
		value := prepareStructuredDataFixtures(t, session, rawValue, rawJSON)
		sql := fmt.Sprintf(
			"create table t(value text); insert into t values ('%s'); select value from t;",
			sqliteStringLiteral(value),
		)

		script := fmt.Appendf(nil,
			"sqlite3 :memory: %s >/tmp/sqlite-value.txt\n",
			shellQuote(sql),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzSQLiteFileCommands(f *testing.F) {
	rt := newFuzzRuntime(f)
	addStructuredDataSeeds(f)
	warmFuzzSQLite(f, rt)

	f.Fuzz(func(t *testing.T, rawValue string, rawJSON []byte) {
		session := newFuzzSession(t, rt)
		value := prepareStructuredDataFixtures(t, session, rawValue, rawJSON)
		sql := fmt.Sprintf(
			"create table if not exists items(value text); insert into items values ('%s'); select value from items order by value;",
			sqliteStringLiteral(value),
		)

		script := fmt.Appendf(nil,
			"sqlite3 -json /tmp/data.db %s >/tmp/sqlite-json.txt\n",
			shellQuote(sql),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzArchiveCommands(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		name string
		data []byte
	}{
		{"alpha", []byte("alpha\nbeta\n")},
		{"json", []byte("{\"value\":1}\n")},
		{"binary", []byte{0x00, 0x01, 0x02, 0xff, '\n'}},
	}
	for _, seed := range seeds {
		f.Add(seed.name, seed.data)
	}

	f.Fuzz(func(t *testing.T, rawName string, rawData []byte) {
		session := newFuzzSession(t, rt)
		name := fuzzPath(rawName)
		payload := clampFuzzData(rawData)

		writeSessionFile(t, session, "/tmp/archive-src/"+name+".txt", payload)
		writeSessionFile(t, session, "/tmp/archive-src/nested/"+name+".bin", append([]byte(nil), payload...))

		script := fmt.Appendf(nil,
			"tar -cf /tmp/archive.tar /tmp/archive-src\n"+
				"tar -tf /tmp/archive.tar >/tmp/archive.list\n"+
				"mkdir -p /tmp/archive-out\n"+
				"tar -xf /tmp/archive.tar -C /tmp/archive-out\n"+
				"tar -czf /tmp/archive.tar.gz /tmp/archive-src\n"+
				"mkdir -p /tmp/archive-out-gz\n"+
				"tar -xzf /tmp/archive.tar.gz -C /tmp/archive-out-gz\n"+
				"gzip -c %s >/tmp/file.txt.gz\n"+
				"gunzip -c /tmp/file.txt.gz >/tmp/file.txt.out\n"+
				"zcat /tmp/file.txt.gz >/tmp/file.txt.zcat\n",
			shellQuote("/tmp/archive-src/"+name+".txt"),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzColumnCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		data     []byte
		sepIndex uint8
		outIndex uint8
	}{
		{[]byte("a b c\nd e f\n"), 0, 0},
		{[]byte("a,,c\nd,e,f\n"), 1, 1},
		{[]byte("alpha:beta\ngamma:delta\n"), 2, 2},
	}
	for _, seed := range seeds {
		f.Add(seed.data, seed.sepIndex, seed.outIndex)
	}

	f.Fuzz(func(t *testing.T, rawData []byte, sepIndex uint8, outIndex uint8) {
		session := newFuzzSession(t, rt)
		inputPath := "/tmp/column-input.txt"
		otherPath := "/tmp/column-other.txt"
		text := normalizeFuzzText(rawData)
		separators := []string{"", ",", ":", "\t", "|", " "}
		outputSeps := []string{"  ", "|", ",", "\t", "", " - "}
		separator := separators[int(sepIndex)%len(separators)]
		outputSep := outputSeps[int(outIndex)%len(outputSeps)]

		writeSessionFile(t, session, inputPath, text)
		writeSessionFile(t, session, otherPath, []byte(strings.ToUpper(string(text))))

		script := fmt.Appendf(nil,
			"column %s >/tmp/column-default.txt\n"+
				"column -t -s %s -o %s -n %s >/tmp/column-table.txt\n"+
				"cat %s | column -c 20 >/tmp/column-stdin.txt\n"+
				"cat %s | column --table - %s >/tmp/column-dash.txt\n",
			shellQuote(inputPath),
			shellQuote(separator),
			shellQuote(outputSep),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(otherPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}

func writeJoinFixtures(t *testing.T, session *Session, text []byte) (leftPath, rightPath string) {
	t.Helper()

	lines := strings.Split(strings.TrimSuffix(string(text), "\n"), "\n")
	keys := make([]string, 0, len(lines))
	for _, line := range lines {
		token := sanitizeFuzzPathComponent(line)
		if token == "" {
			continue
		}
		keys = append(keys, token)
		if len(keys) == 4 {
			break
		}
	}
	if len(keys) == 0 {
		keys = []string{"alpha", "beta"}
	}
	sort.Strings(keys)

	var left strings.Builder
	var right strings.Builder
	for _, key := range keys {
		_, _ = fmt.Fprintf(&left, "%s left-%s\n", key, key)
		_, _ = fmt.Fprintf(&right, "%s right-%s\n", key, key)
	}

	leftPath = path.Join("/tmp", "join-left.txt")
	rightPath = path.Join("/tmp", "join-right.txt")
	writeSessionFile(t, session, leftPath, []byte(left.String()))
	writeSessionFile(t, session, rightPath, []byte(right.String()))
	return leftPath, rightPath
}

func addStructuredDataSeeds(f *testing.F) {
	f.Helper()

	seeds := []struct {
		value string
		raw   []byte
	}{
		{"alpha", []byte(`{"value":"alpha","items":[1,2,3]}`)},
		{"beta", []byte(`{"value":"beta","items":["x","y"]}`)},
		{"gamma", []byte(`not-json`)},
	}
	for _, seed := range seeds {
		f.Add(seed.value, seed.raw)
	}
}

func prepareStructuredDataFixtures(t *testing.T, session *Session, rawValue string, rawJSON []byte) string {
	t.Helper()

	value := sanitizeFuzzToken(rawValue)
	validDoc := map[string]any{
		"value": value,
		"items": []string{value, strings.ToUpper(value)},
	}
	validBytes, err := json.Marshal(validDoc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	writeSessionFile(t, session, "/tmp/input.json", validBytes)
	writeSessionFile(t, session, "/tmp/input.yaml", fmt.Appendf(nil, "value: %s\nitems:\n  - %s\n  - %s\n", value, value, strings.ToUpper(value)))
	writeSessionFile(t, session, "/tmp/raw.json", clampFuzzData(rawJSON))
	return value
}
