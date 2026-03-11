package runtime

import (
	"bytes"
	"strings"
	"testing"
)

func TestSQLite3SupportsMemoryDatabase(t *testing.T) {
	session := newSession(t, nil)

	result := mustExecSession(t, session, `sqlite3 :memory: "create table users(id integer, name text); insert into users values (1, 'alice'), (2, null); select id, name from users order by id;"`+"\n")

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1|alice\n2|\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSQLite3PersistsSandboxDatabaseFilesAcrossExecs(t *testing.T) {
	session := newSession(t, nil)

	first := mustExecSession(t, session, `sqlite3 /tmp/app.db "create table users(name text); insert into users values ('alice'), ('bob');"`+"\n")
	if first.ExitCode != 0 {
		t.Fatalf("first ExitCode = %d, want 0; stderr=%q", first.ExitCode, first.Stderr)
	}

	second := mustExecSession(t, session, `sqlite3 /tmp/app.db "select name from users order by name;"`+"\n")
	if second.ExitCode != 0 {
		t.Fatalf("second ExitCode = %d, want 0; stderr=%q", second.ExitCode, second.Stderr)
	}
	if got, want := second.Stdout, "alice\nbob\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	if data := readSessionFile(t, session, "/tmp/app.db"); len(data) == 0 {
		t.Fatalf("database file was not written")
	}
}

func TestSQLite3ReadsSQLFromStdin(t *testing.T) {
	session := newSession(t, nil)

	result := mustExecSession(t, session, `printf "create table t(x); insert into t values (7); select x from t;" | sqlite3 :memory:`+"\n")

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "7\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSQLite3OutputsJSONAndCSVModes(t *testing.T) {
	session := newSession(t, nil)

	jsonResult := mustExecSession(t, session, `sqlite3 -json :memory: "create table t(id integer, name text); insert into t values (1, 'alice'), (2, null); select id, name from t order by id;"`+"\n")
	if jsonResult.ExitCode != 0 {
		t.Fatalf("json ExitCode = %d, want 0; stderr=%q", jsonResult.ExitCode, jsonResult.Stderr)
	}
	if got, want := jsonResult.Stdout, `[{"id":1,"name":"alice"},{"id":2,"name":null}]`+"\n"; got != want {
		t.Fatalf("json Stdout = %q, want %q", got, want)
	}

	csvResult := mustExecSession(t, session, `sqlite3 -csv -header :memory: "create table t(name text, note text); insert into t values ('alice', 'hi, \"quoted\" world'); select name, note from t;"`+"\n")
	if csvResult.ExitCode != 0 {
		t.Fatalf("csv ExitCode = %d, want 0; stderr=%q", csvResult.ExitCode, csvResult.Stderr)
	}
	if got, want := csvResult.Stdout, "name,note\nalice,\"hi, \"\"quoted\"\" world\"\n"; got != want {
		t.Fatalf("csv Stdout = %q, want %q", got, want)
	}
}

func TestSQLite3SupportsLineAndTableFormatting(t *testing.T) {
	session := newSession(t, nil)

	lineResult := mustExecSession(t, session, `sqlite3 -line :memory: "create table t(id integer, name text); insert into t values (1, 'alice'); select id, name from t;"`+"\n")
	if lineResult.ExitCode != 0 {
		t.Fatalf("line ExitCode = %d, want 0; stderr=%q", lineResult.ExitCode, lineResult.Stderr)
	}
	if got, want := lineResult.Stdout, "id   = 1\nname = alice\n"; got != want {
		t.Fatalf("line Stdout = %q, want %q", got, want)
	}

	tableResult := mustExecSession(t, session, `sqlite3 -table -header :memory: "create table t(id integer, name text); insert into t values (1, 'alice'); select id, name from t;"`+"\n")
	if tableResult.ExitCode != 0 {
		t.Fatalf("table ExitCode = %d, want 0; stderr=%q", tableResult.ExitCode, tableResult.Stderr)
	}
	for _, want := range []string{"+----+-------+", "| id | name  |", "| 1  | alice |"} {
		if !strings.Contains(tableResult.Stdout, want) {
			t.Fatalf("table Stdout = %q, want fragment %q", tableResult.Stdout, want)
		}
	}
}

func TestSQLite3SupportsAdditionalOutputModes(t *testing.T) {
	session := newSession(t, nil)

	markdown := mustExecSession(t, session, `sqlite3 -markdown -header :memory: "create table t(a,b); insert into t values (1,'x'); select * from t;"`+"\n")
	if markdown.ExitCode != 0 {
		t.Fatalf("markdown ExitCode = %d, want 0; stderr=%q", markdown.ExitCode, markdown.Stderr)
	}
	if got, want := markdown.Stdout, "| a | b |\n|---|---|\n| 1 | x |\n"; got != want {
		t.Fatalf("markdown Stdout = %q, want %q", got, want)
	}

	tabs := mustExecSession(t, session, `sqlite3 -tabs -header :memory: "create table t(a,b); insert into t values (1,'x'); select * from t;"`+"\n")
	if tabs.ExitCode != 0 {
		t.Fatalf("tabs ExitCode = %d, want 0; stderr=%q", tabs.ExitCode, tabs.Stderr)
	}
	if got, want := tabs.Stdout, "a\tb\n1\tx\n"; got != want {
		t.Fatalf("tabs Stdout = %q, want %q", got, want)
	}

	box := mustExecSession(t, session, `sqlite3 -box :memory: "create table t(id,name); insert into t values (1,'alice'); select * from t;"`+"\n")
	if box.ExitCode != 0 {
		t.Fatalf("box ExitCode = %d, want 0; stderr=%q", box.ExitCode, box.Stderr)
	}
	for _, want := range []string{"┌", "│ id", "│ 1 "} {
		if !strings.Contains(box.Stdout, want) {
			t.Fatalf("box Stdout = %q, want fragment %q", box.Stdout, want)
		}
	}

	quote := mustExecSession(t, session, `sqlite3 -quote :memory: "select 1 as a, 'x' as b, null as c;"`+"\n")
	if quote.ExitCode != 0 {
		t.Fatalf("quote ExitCode = %d, want 0; stderr=%q", quote.ExitCode, quote.Stderr)
	}
	if got, want := quote.Stdout, "1,'x',NULL\n"; got != want {
		t.Fatalf("quote Stdout = %q, want %q", got, want)
	}

	html := mustExecSession(t, session, `sqlite3 -html -header :memory: "select '<tag>' as c;"`+"\n")
	if html.ExitCode != 0 {
		t.Fatalf("html ExitCode = %d, want 0; stderr=%q", html.ExitCode, html.Stderr)
	}
	if got, want := html.Stdout, "<TR><TH>c</TH>\n</TR>\n<TR><TD>&lt;tag&gt;</TD>\n</TR>\n"; got != want {
		t.Fatalf("html Stdout = %q, want %q", got, want)
	}

	ascii := mustExecSession(t, session, `sqlite3 -ascii -header :memory: "create table t(a,b); insert into t values (1,2); select * from t;"`+"\n")
	if ascii.ExitCode != 0 {
		t.Fatalf("ascii ExitCode = %d, want 0; stderr=%q", ascii.ExitCode, ascii.Stderr)
	}
	if got, want := ascii.Stdout, "a\x1fb\x1e1\x1f2\x1e"; got != want {
		t.Fatalf("ascii Stdout = %q, want %q", got, want)
	}
}

func TestSQLite3QuoteModeMatchesUpstreamQuotingAndFloatFormatting(t *testing.T) {
	session := newSession(t, nil)

	result := mustExecSession(t, session, `sqlite3 -quote -header :memory: "select 'O''Reilly' as author, 0.1 as ratio;"`+"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "'author','ratio'\n'O'Reilly',0.10000000000000001\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSQLite3RunsCmdBeforeMainSQL(t *testing.T) {
	session := newSession(t, nil)

	result := mustExecSession(t, session, `sqlite3 -cmd "create table t(x); insert into t values (41);" :memory: "insert into t values (42); select x from t order by x;"`+"\n")

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "41\n42\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSQLite3ReadonlyRejectsWritesAndDoesNotPersist(t *testing.T) {
	session := newSession(t, nil)

	setup := mustExecSession(t, session, `sqlite3 /tmp/readonly.db "create table t(x); insert into t values (1);"`+"\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0; stderr=%q", setup.ExitCode, setup.Stderr)
	}
	before := append([]byte(nil), readSessionFile(t, session, "/tmp/readonly.db")...)

	result := mustExecSession(t, session, `sqlite3 -readonly /tmp/readonly.db "insert into t values (2); select x from t order by x;"`+"\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "attempt to write a readonly database") && !strings.Contains(result.Stderr, "not authorized") {
		t.Fatalf("Stderr = %q, want readonly denial", result.Stderr)
	}
	after := readSessionFile(t, session, "/tmp/readonly.db")
	if !bytes.Equal(after, before) {
		t.Fatalf("readonly database bytes changed")
	}
}

func TestSQLite3ContinuesWithoutBailButReturnsFailure(t *testing.T) {
	session := newSession(t, nil)

	result := mustExecSession(t, session, `sqlite3 :memory: "create table t(x unique); insert into t values (1); select x from t; insert into t values (1); select 2;"`+"\n")

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1\n2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "constraint failed") {
		t.Fatalf("Stderr = %q, want unique-constraint error", result.Stderr)
	}
}

func TestSQLite3BailStopsOnFirstError(t *testing.T) {
	session := newSession(t, nil)

	result := mustExecSession(t, session, `sqlite3 -bail :memory: "create table t(x unique); insert into t values (1); select x from t; insert into t values (1); select 2;"`+"\n")

	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if strings.Contains(result.Stdout, "2\n") {
		t.Fatalf("Stdout = %q, want execution to stop before third statement", result.Stdout)
	}
}

func TestSQLite3RejectsSandboxEscapeSQL(t *testing.T) {
	session := newSession(t, nil)

	loadExt := mustExecSession(t, session, `sqlite3 :memory: "select load_extension('x');"`+"\n")
	if loadExt.ExitCode != 1 {
		t.Fatalf("load_extension ExitCode = %d, want 1; stderr=%q", loadExt.ExitCode, loadExt.Stderr)
	}
	if !strings.Contains(loadExt.Stderr, "load_extension() is disabled") {
		t.Fatalf("load_extension Stderr = %q, want explicit denial", loadExt.Stderr)
	}

	attach := mustExecSession(t, session, `sqlite3 :memory: "attach database '/tmp/escape.db' as other;"`+"\n")
	if attach.ExitCode != 1 {
		t.Fatalf("attach ExitCode = %d, want 1; stderr=%q", attach.ExitCode, attach.Stderr)
	}
	if !strings.Contains(attach.Stderr, "ATTACH is disabled") {
		t.Fatalf("attach Stderr = %q, want attach denial", attach.Stderr)
	}

	vacuum := mustExecSession(t, session, `sqlite3 :memory: "vacuum into '/tmp/out.db';"`+"\n")
	if vacuum.ExitCode != 1 {
		t.Fatalf("vacuum ExitCode = %d, want 1; stderr=%q", vacuum.ExitCode, vacuum.Stderr)
	}
	if !strings.Contains(vacuum.Stderr, "VACUUM is disabled") {
		t.Fatalf("vacuum Stderr = %q, want vacuum denial", vacuum.Stderr)
	}
}

func TestSQLite3MissingWritableDatabaseDoesNotCreateEmptyFile(t *testing.T) {
	session := newSession(t, nil)

	result := mustExecSession(t, session, `sqlite3 /tmp/missing.db "select * from missing;"`+"\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "no such table: missing") {
		t.Fatalf("Stderr = %q, want missing-table error", result.Stderr)
	}

	check := mustExecSession(t, session, `test -e /tmp/missing.db; echo $?`+"\n")
	if got, want := check.Stdout, "1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
