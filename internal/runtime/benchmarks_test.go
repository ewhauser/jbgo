package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func BenchmarkNewSession(b *testing.B) {
	rt := newRuntime(b, nil)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		session, err := rt.NewSession(ctx)
		if err != nil {
			b.Fatalf("NewSession() error = %v", err)
		}
		if _, err := session.FileSystem().Stat(ctx, "/bin/echo"); err != nil {
			b.Fatalf("Stat(/bin/echo) error = %v", err)
		}
	}
}

func BenchmarkRuntimeRunSimpleScript(b *testing.B) {
	rt := newRuntime(b, nil)

	benchmarkRuntimeRun(b, rt, &ExecutionRequest{
		Script: "echo hi\npwd\n",
	}, "hi\n/home/agent\n")
}

func BenchmarkSessionExecWarmSimpleScript(b *testing.B) {
	rt := newRuntime(b, nil)
	session := mustNewBenchmarkSession(b, rt)

	benchmarkSessionExec(b, session, &ExecutionRequest{
		Script: "echo hi\npwd\n",
	}, "hi\n/home/agent\n")
}

func BenchmarkWorkflowCodebaseExploration(b *testing.B) {
	files, totalBytes := codebaseExplorationBenchmarkFiles()
	rt := newSeededRuntime(b, files)
	b.SetBytes(totalBytes)

	benchmarkWorkflow(b, rt,
		benchmarkWorkflowStep{
			req: ExecutionRequest{
				WorkDir: "/home/agent/project",
				Script: "" +
					"find src -type f > inventory.txt\n" +
					"grep -r \"TODO\" src > todos.txt\n",
			},
			wantStdout: "",
		},
		benchmarkWorkflowStep{
			req: ExecutionRequest{
				WorkDir: "/home/agent/project",
				Script: "" +
					"grep -c \"\\\\.go\" inventory.txt\n" +
					"grep -c \"TODO\" todos.txt\n",
			},
			wantStdout: "300\n30\n",
		},
	)
}

func BenchmarkWorkflowRefactorPreparation(b *testing.B) {
	files, totalBytes := refactorPreparationBenchmarkFiles()
	rt := newSeededRuntime(b, files)
	b.SetBytes(totalBytes)

	benchmarkWorkflow(b, rt,
		benchmarkWorkflowStep{
			req: ExecutionRequest{
				WorkDir: "/home/agent/project",
				Script: "" +
					"cp -r src snapshot\n" +
					"mv docs notes\n" +
					"find snapshot -name '*.txt' -type f > snapshot.files\n" +
					"grep -r \"TODO\" notes > notes.todo\n",
			},
			wantStdout: "",
		},
		benchmarkWorkflowStep{
			req: ExecutionRequest{
				WorkDir: "/home/agent/project",
				Script: "" +
					"grep -c '^' snapshot.files\n" +
					"grep -c 'TODO' notes.todo\n",
			},
			wantStdout: "400\n40\n",
		},
	)
}

func BenchmarkCommandFindTree(b *testing.B) {
	files, totalBytes := findBenchmarkFiles()
	rt := newSeededRuntime(b, files)
	session := mustNewBenchmarkSession(b, rt)
	b.SetBytes(totalBytes)

	benchmarkSessionExec(b, session, &ExecutionRequest{
		Script: "find /bench/tree -name '*.txt' -type f | grep -c '^'\n",
	}, "1000\n")
}

func BenchmarkCommandRGRecursive(b *testing.B) {
	files, totalBytes := rgBenchmarkFiles()
	rt := newSeededRuntime(b, files)
	session := mustNewBenchmarkSession(b, rt)
	b.SetBytes(totalBytes)

	benchmarkSessionExec(b, session, &ExecutionRequest{
		Script: "rg -l needle /bench/search | grep -c '^'\n",
	}, "40\n")
}

func BenchmarkCommandSortUniq(b *testing.B) {
	files, totalBytes := sortBenchmarkFiles()
	rt := newSeededRuntime(b, files)
	session := mustNewBenchmarkSession(b, rt)
	b.SetBytes(totalBytes)

	benchmarkSessionExec(b, session, &ExecutionRequest{
		Script: "sort /bench/sort/input.txt | uniq | grep -c '^'\n",
	}, "5000\n")
}

func BenchmarkCommandTarGzipRoundTrip(b *testing.B) {
	files, totalBytes := archiveBenchmarkFiles()
	rt := newSeededRuntime(b, files)
	b.SetBytes(totalBytes)

	benchmarkFreshSessionExec(b, rt, &ExecutionRequest{
		Script: "" +
			"tar -czf /tmp/archive.tar.gz /workspace/archive\n" +
			"mkdir -p /tmp/out\n" +
			"tar -xzf /tmp/archive.tar.gz -C /tmp/out\n" +
			"find /tmp/out/workspace/archive -type f | grep -c '^'\n",
	}, "200\n")
}

func codebaseExplorationBenchmarkFiles() (files map[string]string, totalBytes int64) {
	files = make(map[string]string, 300)
	for i := range 300 {
		todo := ""
		if i < 30 {
			todo = fmt.Sprintf("// TODO: review benchmark file %03d\n", i)
		}
		content := fmt.Sprintf(
			"package pkg%02d\n%sfunc File%03d() int { return %d }\n",
			i%12,
			todo,
			i,
			i,
		)
		name := fmt.Sprintf("/home/agent/project/src/pkg%02d/file%03d.go", i%12, i)
		files[name] = content
		totalBytes += int64(len(content))
	}
	return files, totalBytes
}

func refactorPreparationBenchmarkFiles() (files map[string]string, totalBytes int64) {
	files = make(map[string]string, 440)
	for i := range 400 {
		content := fmt.Sprintf("module-%03d\npayload-%03d\n", i, i)
		name := fmt.Sprintf("/home/agent/project/src/module%02d/file%03d.txt", i%20, i)
		files[name] = content
		totalBytes += int64(len(content))
	}
	for i := range 40 {
		content := fmt.Sprintf("# Note %02d\nTODO: follow up on item %02d\n", i, i)
		name := fmt.Sprintf("/home/agent/project/docs/note%02d.md", i)
		files[name] = content
		totalBytes += int64(len(content))
	}
	return files, totalBytes
}

func findBenchmarkFiles() (files map[string]string, totalBytes int64) {
	files = make(map[string]string, 1000)
	for i := range 1000 {
		content := fmt.Sprintf("find-target-%04d\n", i)
		name := fmt.Sprintf("/bench/tree/dir%02d/sub%02d/file%04d.txt", i%25, i%10, i)
		files[name] = content
		totalBytes += int64(len(content))
	}
	return files, totalBytes
}

func rgBenchmarkFiles() (files map[string]string, totalBytes int64) {
	files = make(map[string]string, 200)
	for i := range 200 {
		var body strings.Builder
		for line := range 250 {
			if line == 125 && i < 40 {
				body.WriteString("needle benchmark target\n")
				continue
			}
			fmt.Fprintf(&body, "file-%03d line-%03d alpha beta gamma\n", i, line)
		}
		content := body.String()
		name := fmt.Sprintf("/bench/search/group%02d/file%03d.txt", i%20, i)
		files[name] = content
		totalBytes += int64(len(content))
	}
	return files, totalBytes
}

func sortBenchmarkFiles() (files map[string]string, totalBytes int64) {
	var body strings.Builder
	for repeat := 3; repeat >= 0; repeat-- {
		for i := 4999; i >= 0; i-- {
			fmt.Fprintf(&body, "value-%04d\n", i)
		}
	}
	content := body.String()
	files = map[string]string{
		"/bench/sort/input.txt": content,
	}
	totalBytes = int64(len(content))
	return files, totalBytes
}

func archiveBenchmarkFiles() (files map[string]string, totalBytes int64) {
	files = make(map[string]string, 200)
	for i := range 200 {
		pattern := fmt.Sprintf("archive-file-%03d-", i)
		repeats := 20480/len(pattern) + 1
		content := strings.Repeat(pattern, repeats)
		content = content[:20480]
		name := fmt.Sprintf("/workspace/archive/dir%02d/file%03d.txt", i%20, i)
		files[name] = content
		totalBytes += int64(len(content))
	}
	return files, totalBytes
}
