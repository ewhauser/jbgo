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

		script := []byte(fmt.Sprintf(
			"touch %s\ncp %s %s\nmv %s %s\nln -s -f %s %s\nreadlink %s >/tmp/readlink.out\nstat %s >/tmp/stat.out\nbasename %s >/tmp/base.out\ndirname %s >/tmp/dir.out\nchmod 600 %s\nmkdir -p /tmp/fuzz-empty/sub\nrmdir /tmp/fuzz-empty/sub\nfile -b %s >/tmp/file.out\ndu -a /tmp >/tmp/du.out\ntree -a -L 2 /tmp >/tmp/tree.out\nrm %s %s %s\n",
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
		))

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

		script := []byte(fmt.Sprintf(
			"sort %s >/tmp/sort.txt || true\nuniq /tmp/sort.txt >/tmp/uniq.txt || true\ncut -c 1-8 %s >/tmp/cut.txt || true\nsed -n '1,3p' %s >/tmp/sed.txt || true\ngrep -n %s %s >/tmp/grep.txt || true\nrg -n %s /tmp >/tmp/rg.txt || true\nawk '{print NF}' %s >/tmp/awk.txt || true\nhead -n 3 %s >/tmp/head.txt || true\ntail -n 3 %s >/tmp/tail.txt || true\nwc %s >/tmp/wc.txt\ntr 'a-z' 'A-Z' < %s >/tmp/tr.txt || true\nrev %s >/tmp/rev.txt || true\nnl -ba %s >/tmp/nl.txt || true\ntac %s >/tmp/tac.txt || true\nsplit -l 2 %s /tmp/split- || true\npaste %s %s >/tmp/paste.txt || true\ncomm -12 /tmp/sort.txt /tmp/sort.txt >/tmp/comm.txt || true\njoin %s %s >/tmp/join.txt || true\ndiff -u %s %s >/tmp/diff.txt || true\nbase64 %s | base64 -d >/tmp/base64.txt || true\n",
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
			shellQuote(inputPath),
			shellQuote(joinLeftPath),
			shellQuote(joinRightPath),
			shellQuote(inputPath),
			shellQuote(otherPath),
			shellQuote(inputPath),
		))

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

		script := []byte(fmt.Sprintf(
			"cat %s | tee /tmp/tee.txt >/tmp/tee.out\nenv -i ONLY=%s printenv ONLY >/tmp/env.txt\nprintenv HOME >/tmp/printenv.txt\nwhich echo >/tmp/which.txt\nhelp -s pwd >/tmp/help.txt\ndate -u -d 2024-01-02T03:04:05 +%%F >/tmp/date.txt\nsleep 0.001\ntimeout 0.01 sleep 1 || true\nprintf 'echo from-stdin\\n' | sh >/tmp/sh.txt\nbash -c 'echo \"$1\"' ignored %s >/tmp/bash.txt\ncat %s | xargs -n 1 echo >/tmp/xargs.txt || true\ntrue\n/bin/false || true\n",
			shellQuote(inputPath),
			shellQuote(value),
			shellQuote(value),
			shellQuote(inputPath),
		))

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzDataCommands(f *testing.F) {
	rt := newFuzzRuntime(f)

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

	f.Fuzz(func(t *testing.T, rawValue string, rawJSON []byte) {
		session := newFuzzSession(t, rt)
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
		writeSessionFile(t, session, "/tmp/input.yaml", []byte(fmt.Sprintf("value: %s\nitems:\n  - %s\n  - %s\n", value, value, strings.ToUpper(value))))
		writeSessionFile(t, session, "/tmp/raw.json", clampFuzzData(rawJSON))

		script := []byte(fmt.Sprintf(
			"jq -r '.value' /tmp/input.json >/tmp/jq-value.txt\njq -c '.items' /tmp/input.json >/tmp/jq-items.txt\njq -n --arg value %s '{value:$value}' >/tmp/jq-build.txt\njq '.value' /tmp/raw.json >/tmp/jq-raw.txt || true\nyq '.value' /tmp/input.yaml >/tmp/yq-value.txt\nyq -p json -o json '.items' /tmp/input.json >/tmp/yq-items.txt\nyq -n '.value = \"built\"' >/tmp/yq-build.txt\nsqlite3 :memory: \"create table t(value text); insert into t values ('%s'); select value from t;\" >/tmp/sqlite-value.txt\nsqlite3 -json /tmp/data.db \"create table if not exists items(value text); insert into items values ('%s'); select value from items order by value;\" >/tmp/sqlite-json.txt\nbase64 /tmp/input.json | base64 -d >/tmp/base64-json.txt || true\n",
			shellQuote(value),
			sqliteStringLiteral(value),
			sqliteStringLiteral(value),
		))

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
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
