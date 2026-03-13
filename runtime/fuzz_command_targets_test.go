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
		unlinkPath := fuzzPath(rawName) + ".unlink"
		linkPath := fuzzPath(rawName) + ".ln"
		data := clampFuzzData(rawData)

		writeSessionFile(t, session, inputPath, data)

		script := fmt.Appendf(nil,
			"touch %s\ncp -pv %s %s\nmv -v %s %s\ncp %s %s\nunlink %s\nln -s -f %s %s\nreadlink %s >/tmp/readlink.out\nstat %s >/tmp/stat.out\nbasename --suffix=.moved %s >/tmp/base.out\ndirname %s >/tmp/dir.out\nchmod 600 %s\nchown 123:456 %s\nchown -h 321:654 %s || true\nchgrp 456 %s\nchgrp -h 654 %s || true\nmkdir -p /tmp/fuzz-empty/sub\nrmdir /tmp/fuzz-empty/sub\nmktemp /tmp/fuzz.XXXX >/tmp/mktemp-file.out\nmktemp -d /tmp/fuzzdir.XXXX >/tmp/mktemp-dir.out\nmktemp -u --suffix=.txt /tmp/fuzzdry.XXXX >/tmp/mktemp-dry.out\nmktemp --tmpdir=. fuzz.XXXX >/tmp/mktemp-rel.out\nfile --brief %s >/tmp/file.out\nrm %s %s %s\n",
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(copyPath),
			shellQuote(copyPath),
			shellQuote(movedPath),
			shellQuote(movedPath),
			shellQuote(unlinkPath),
			shellQuote(unlinkPath),
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
			shellQuote(movedPath),
			shellQuote(linkPath),
			shellQuote(inputPath),
			shellQuote(linkPath),
			shellQuote(movedPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzRealpathCommand(f *testing.F) {
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
		linkPath := fuzzPath(rawName) + ".ln"
		missingPath := fuzzPath(rawName) + ".missing"
		dirPath := path.Dir(inputPath)
		data := clampFuzzData(rawData)

		writeSessionFile(t, session, inputPath, data)

		script := fmt.Appendf(nil,
			"mkdir -p %s\nln -s -f %s %s\nrealpath %s >/tmp/realpath-basic.out\nrealpath -z %s %s >/tmp/realpath-zero.out\nrealpath -s %s >/tmp/realpath-strip.out\nrealpath -E %s.leaf >/tmp/realpath-canonical.out\nrealpath -e %s >/tmp/realpath-existing.out\nrealpath -m %s >/tmp/realpath-missing.out\nrealpath -L %s/.. >/tmp/realpath-logical.out || true\nrealpath -P %s/.. >/tmp/realpath-physical.out || true\nrealpath --relative-to=%s %s >/tmp/realpath-relative.out\nrealpath --relative-base=%s %s >/tmp/realpath-relative-base.out\nrealpath -q %s >/tmp/realpath-quiet.out || true\n",
			shellQuote(dirPath),
			shellQuote(inputPath),
			shellQuote(linkPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(linkPath),
			shellQuote(linkPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(missingPath),
			shellQuote(linkPath),
			shellQuote(linkPath),
			shellQuote(dirPath),
			shellQuote(inputPath),
			shellQuote(dirPath),
			shellQuote(inputPath),
			shellQuote(missingPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}

func FuzzTruncateCommand(f *testing.F) {
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
		referencePath := fuzzPath(rawName) + ".ref"
		data := clampFuzzData(rawData)

		writeSessionFile(t, session, inputPath, data)
		writeSessionFile(t, session, referencePath, data)

		script := fmt.Appendf(nil,
			"truncate -s 0 %s\ntruncate -s +1 %s\ntruncate -s %%4 %s\ntruncate -c -s 8 %s.missing\ntruncate -r %s %s.copy\ntruncate -r %s -s +2 %s.plus\n",
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(referencePath),
			shellQuote(inputPath),
			shellQuote(referencePath),
			shellQuote(inputPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
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

func FuzzCompatPredicateCommands(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		name  string
		data  []byte
		token string
	}{
		{"alpha", []byte("hello\n"), "match"},
		{"notes-1", []byte("# title\nbody\n"), "value"},
		{"data.bin", []byte{0x00, 0x01, 0x02, 0x03, 0xff}, "binary"},
	}
	for _, seed := range seeds {
		f.Add(seed.name, seed.data, seed.token)
	}

	f.Fuzz(func(t *testing.T, rawName string, rawData []byte, rawToken string) {
		session := newFuzzSession(t, rt)
		inputPath := fuzzPath(rawName) + ".txt"
		linkPath := fuzzPath(rawName) + ".link"
		dirPath := path.Dir(inputPath)
		data := clampFuzzData(rawData)
		token := sanitizeFuzzToken(rawToken)
		if token == "" {
			token = "value"
		}

		writeSessionFile(t, session, inputPath, data)

		script := fmt.Appendf(nil,
			"dir %s >/tmp/dir.out || true\nvdir %s >/tmp/vdir.out || true\nvdir -C %s >/tmp/vdir-columns.out || true\nlink %s %s || true\ntest -e %s || true\ntest %s = %s || true\n[ -s %s ] || true\n",
			shellQuote(dirPath),
			shellQuote(dirPath),
			shellQuote(dirPath),
			shellQuote(inputPath),
			shellQuote(linkPath),
			shellQuote(inputPath),
			shellQuote(token),
			shellQuote(token),
			shellQuote(inputPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
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
			"printf '1,3p\\n' >/tmp/sed.fuzz\nsort %s >/tmp/sort.txt || true\nuniq --ignore-case /tmp/sort.txt >/tmp/uniq.txt || true\ncut --only-delimited -c 1-8 %s >/tmp/cut.txt || true\nsed -f /tmp/sed.fuzz %s >/tmp/sed.txt || true\ngrep -n %s %s >/tmp/grep.txt || true\nrg -n %s /tmp >/tmp/rg.txt || true\nhead --bytes=3 %s >/tmp/head.txt || true\ntail --bytes=3 %s >/tmp/tail.txt || true\nwc %s >/tmp/wc.txt\ntr --delete '[:digit:]' < %s >/tmp/tr.txt || true\nrev %s >/tmp/rev.txt || true\nnl -ba -n rz %s >/tmp/nl.txt || true\ntac %s >/tmp/tac.txt || true\nsplit -n 3 --additional-suffix=.part %s /tmp/split- || true\ncat /tmp/split-aa.part >/tmp/split.txt || true\npaste --serial --delimiters=, %s >/tmp/paste.txt || true\ncomm -1 /tmp/sort.txt /tmp/sort.txt >/tmp/comm.txt || true\njoin %s %s >/tmp/join.txt || true\ndiff -u %s %s >/tmp/diff.txt || true\nbase64 --wrap=0 %s | base64 -d >/tmp/base64.txt || true\ncat --number %s >/tmp/cat.txt || true\nseq 1 1 5 >/tmp/seq.txt || true\nseq -w 1 5 >/tmp/seq-width.txt || true\nseq -f '%%.2f' 0 0.5 2 >/tmp/seq-format.txt || true\n",
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
			"cat %s | tee /tmp/tee.txt >/tmp/tee.out\ncat %s | tee -a /tmp/tee.txt >/tmp/tee-append.out\ncat %s | tee -ip /tmp/tee-flags.txt >/tmp/tee-flags.out\ncat %s | tee --output-error /tmp/tee-output-error.txt >/tmp/tee-output-error.out\nenv --ignore-environment ONLY=%s printenv ONLY >/tmp/env.txt\nprintenv HOME >/tmp/printenv.txt\nwhich echo >/tmp/which.txt\nhelp -s pwd >/tmp/help.txt\ndate -u -d 2024-01-02T03:04:05 +%%F >/tmp/date.txt\ndate --utc --date 2024-01-02T03:04:05 +%%Z >/tmp/date-utc.txt\ndate --date 2024-01-02T03:04:05 --iso-8601 >/tmp/date-iso.txt\ndate --date 2024-01-02T03:04:05 --rfc-email >/tmp/date-rfc.txt\nid >/tmp/id.txt\nid -u >/tmp/id-u.txt\nid -Gn >/tmp/id-gn.txt\nid -A >/tmp/id-audit.txt || true\nwhoami >/tmp/whoami.txt\narch >/tmp/arch.txt\narch -V >/tmp/arch-version.txt\narch --help >/tmp/arch-help.txt\narch --ver >/tmp/arch-infer-version.txt\nTTY=/dev/pts/0 tty >/tmp/tty.txt\nTTY=/dev/pts/0 tty -s >/tmp/tty-silent.txt\nTTY=tty1 tty --quiet >/tmp/tty-quiet.txt\ntty --version >/tmp/tty-version.txt\nuptime >/tmp/uptime.txt\nuptime -s >/tmp/uptime-since.txt\nuptime -p >/tmp/uptime-pretty.txt\ntimeout 0.005 yes %s > /tmp/yes.txt || true\nsleep 0.001\ntrue\n/bin/false || true\n",
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(inputPath),
			shellQuote(value),
			shellQuote(value),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSuccessfulFuzzExecution(t, script, result, err)
	})
}

func FuzzWhoCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte(""),
		[]byte("short"),
		append(make([]byte, 384), []byte("user-process")...),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, rawData []byte) {
		session := newFuzzSession(t, rt)
		data := clampFuzzData(rawData)
		utmpPath := "/tmp/who.utmp"

		writeSessionFile(t, session, utmpPath, data)
		writeSessionFile(t, session, "/var/run/utmp", data)
		writeSessionFile(t, session, "/dev/pts/0", []byte("tty\n"))

		script := fmt.Appendf(nil,
			"who %s >/tmp/who-default.out || true\nwho -q %s >/tmp/who-count.out || true\nwho -uT %s >/tmp/who-users.out || true\nwho --lookup -a %s >/tmp/who-all.out || true\nwho -m %s </dev/pts/0 >/tmp/who-m.out || true\nwho am i </dev/pts/0 >/tmp/who-am-i.out || true\n",
			shellQuote(utmpPath),
			shellQuote(utmpPath),
			shellQuote(utmpPath),
			shellQuote(utmpPath),
			shellQuote(utmpPath),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}

func FuzzEchoCommand(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		data  []byte
		value string
	}{
		{[]byte("alpha beta\n"), "VALUE"},
		{[]byte("one\ntwo\nthree\n"), "nested-value"},
		{[]byte{0x00, 0x01, 0x1b, 0xff}, "with spaces"},
	}
	for _, seed := range seeds {
		f.Add(seed.data, seed.value)
	}

	f.Fuzz(func(t *testing.T, rawData []byte, rawValue string) {
		session := newFuzzSession(t, rt)
		value := sanitizeFuzzToken(rawValue)
		escaped := fuzzEchoEscapedLiteral(rawData)

		script := fmt.Appendf(nil,
			"echo %s >/tmp/echo-default.txt\n"+
				"echo -n %s >/tmp/echo-no-newline.txt\n"+
				"echo -e %s >/tmp/echo-escapes.txt\n"+
				"echo -E %s >/tmp/echo-literal.txt\n"+
				"POSIXLY_CORRECT=1 echo -n -E %s >/tmp/echo-posix.txt\n"+
				"echo -- %s >/tmp/echo-double-dash.txt\n"+
				"echo --version >/tmp/echo-version.txt\n"+
				"echo --help >/tmp/echo-help.txt\n",
			shellQuote(value),
			shellQuote(value),
			shellQuote(escaped),
			shellQuote(escaped),
			shellQuote(escaped),
			shellQuote(value),
		)

		result, err := runFuzzSessionScript(t, session, script)
		assertSecureFuzzOutcome(t, script, result, err)
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

func fuzzEchoEscapedLiteral(rawData []byte) string {
	data := clampFuzzData(rawData)
	if len(data) == 0 {
		return `\n`
	}

	hexDigits := "0123456789abcdef"
	var b strings.Builder
	for i, ch := range data {
		switch i % 6 {
		case 0:
			b.WriteString(`\x`)
			b.WriteByte(hexDigits[ch>>4])
			b.WriteByte(hexDigits[ch&0x0f])
		case 1:
			b.WriteString(`\0`)
			b.WriteByte('0' + ((ch >> 6) & 0x07))
			b.WriteByte('0' + ((ch >> 3) & 0x07))
			b.WriteByte('0' + (ch & 0x07))
		case 2:
			b.WriteString(`\\`)
		case 3:
			b.WriteString(`\n`)
		case 4:
			b.WriteByte('A' + (ch % 26))
		default:
			b.WriteByte('a' + (ch % 26))
		}
		if i == len(data)/2 {
			b.WriteString(`\c`)
			break
		}
	}
	return b.String()
}

func FuzzDataCommands(f *testing.F) {
	rt := newFuzzRuntime(f)
	addStructuredDataSeeds(f)

	f.Fuzz(func(t *testing.T, rawValue string, rawJSON []byte) {
		session := newFuzzSession(t, rt)
		_ = prepareStructuredDataFixtures(t, session, rawValue, rawJSON)

		script := []byte(
			"base64 /tmp/input.json | base64 -d >/tmp/base64-json.txt || true\n" +
				"od -An -tx1 /tmp/input.json >/tmp/od-hex.txt || true\n" +
				"od -An -c /tmp/input.json >/tmp/od-char.txt || true\n" +
				"factor 12 99991 >/tmp/factor-args.txt\n" +
				"factor -h 16 81 >/tmp/factor-exp.txt\n" +
				"cat /tmp/input.json | factor >/tmp/factor-stdin.txt || true\n" +
				"md5sum /tmp/input.json >/tmp/md5-file.txt\n" +
				"cat /tmp/input.json | md5sum >/tmp/md5-stdin.txt\n" +
				"md5sum /tmp/input.json >/tmp/md5-checksums.txt\n" +
				"md5sum -c /tmp/md5-checksums.txt >/tmp/md5-check.txt || true\n" +
				"sha1sum /tmp/input.json >/tmp/sha1-file.txt\n" +
				"cat /tmp/input.json | sha1sum >/tmp/sha1-stdin.txt\n" +
				"sha1sum /tmp/input.json >/tmp/sha1-checksums.txt\n" +
				"sha1sum -c /tmp/sha1-checksums.txt >/tmp/sha1-check.txt || true\n" +
				"sha256sum /tmp/input.json >/tmp/sha256-file.txt\n" +
				"cat /tmp/input.json | sha256sum >/tmp/sha256-stdin.txt\n" +
				"sha256sum /tmp/input.json >/tmp/sha256-checksums.txt\n" +
				"sha256sum -c /tmp/sha256-checksums.txt >/tmp/sha256-check.txt || true\n" +
				"sha224sum /tmp/input.json >/tmp/sha224-file.txt\n" +
				"sha384sum /tmp/input.json >/tmp/sha384-file.txt\n" +
				"sha512sum /tmp/input.json >/tmp/sha512-file.txt\n" +
				"b2sum /tmp/input.json >/tmp/b2-file.txt\n" +
				"b2sum --length=128 /tmp/input.json >/tmp/b2-128-file.txt\n" +
				"b2sum /tmp/input.json >/tmp/b2-checksums.txt\n" +
				"b2sum -c /tmp/b2-checksums.txt >/tmp/b2-check.txt || true\n" +
				"cksum /tmp/input.json >/tmp/cksum-crc.txt\n" +
				"cksum -a md5 /tmp/input.json >/tmp/cksum-md5.txt\n" +
				"cksum -a sha2 -l 256 /tmp/input.json >/tmp/cksum-sha256.txt\n" +
				"cksum -a blake2b -l 128 /tmp/input.json >/tmp/cksum-b2-128.txt\n" +
				"cksum -a md5 /tmp/input.json >/tmp/cksum-md5-checksums.txt\n" +
				"cksum -c /tmp/cksum-md5-checksums.txt >/tmp/cksum-md5-check.txt || true\n",
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
	writeSessionFile(t, session, "/tmp/raw.json", clampFuzzData(rawJSON))
	return value
}
