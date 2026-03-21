package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/policy"
)

func TestMaxLoopIterationsEnforcedInWhile(t *testing.T) {
	t.Parallel()
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 10})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "while true; do\n  :\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "while loop: too many iterations") {
		t.Fatalf("Stderr = %q, want while-loop limit message", result.Stderr)
	}
}

func TestMaxLoopIterationsEnforcedInFor(t *testing.T) {
	t.Parallel()
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 5})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "for i in 1 2 3 4 5 6; do\n  :\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "for loop: too many iterations") {
		t.Fatalf("Stderr = %q, want for-loop limit message", result.Stderr)
	}
}

func TestMaxLoopIterationsEnforcedInUntil(t *testing.T) {
	t.Parallel()
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 5})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "until false; do\n  :\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "until loop: too many iterations") {
		t.Fatalf("Stderr = %q, want until-loop limit message", result.Stderr)
	}
}

func TestMaxLoopIterationsEnforcedInNestedLoops(t *testing.T) {
	t.Parallel()
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 5})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "for i in 1 2 3; do\n  for j in 1 2 3 4 5 6; do\n    :\n  done\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "for loop: too many iterations") {
		t.Fatalf("Stderr = %q, want nested-loop limit message", result.Stderr)
	}
}

func TestMaxLoopIterationsEnforcedInCStyleFor(t *testing.T) {
	t.Parallel()
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 5})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "for ((;;)); do\n  :\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "for loop: too many iterations") {
		t.Fatalf("Stderr = %q, want C-style for-loop limit message", result.Stderr)
	}
}

func TestMaxLoopIterationsAllowsLoopsWithinLimit(t *testing.T) {
	t.Parallel()
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 100})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "for i in 1 2 3 4 5; do\n  echo $i\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got, want := result.Stdout, "1\n2\n3\n4\n5\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDebugTrapSkipsLoopBudgetHelper(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		script string
		want   string
	}{
		{
			name: "for-each",
			script: `debuglog() {
  echo "  [$@]"
}
trap 'debuglog $LINENO' DEBUG

for x in 1 2; do
  echo x=$x
done

echo ok
`,
			want: "" +
				"  [6]\n" +
				"  [7]\n" +
				"x=1\n" +
				"  [6]\n" +
				"  [7]\n" +
				"x=2\n" +
				"  [10]\n" +
				"ok\n",
		},
		{
			name: "for-expr",
			script: `debuglog() {
  echo "  [$@]"
}
trap 'debuglog $LINENO' DEBUG

for (( i =3 ; i < 5; ++i )); do
  echo i=$i
done

echo ok
`,
			want: "" +
				"  [6]\n" +
				"  [6]\n" +
				"  [7]\n" +
				"i=3\n" +
				"  [6]\n" +
				"  [6]\n" +
				"  [7]\n" +
				"i=4\n" +
				"  [6]\n" +
				"  [6]\n" +
				"  [10]\n" +
				"ok\n",
		},
		{
			name: "while-control-flow",
			script: `debuglog() {
  echo "  [$@]"
}
trap 'debuglog $LINENO' DEBUG

while true; do
  echo hello
  break
done
`,
			want: "" +
				"  [6]\n" +
				"  [7]\n" +
				"hello\n" +
				"  [8]\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 100})

			result, err := rt.Run(context.Background(), &ExecutionRequest{
				Script: tc.script,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, result.Stderr)
			}
			if got := result.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestMaxLoopIterationsDoNotLeakAcrossTopLevelChunks(t *testing.T) {
	t.Parallel()
	rt := newRuntimeWithLimits(t, policy.Limits{MaxLoopIterations: 3})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "for i in 1 2 3; do\n  echo a$i\ndone\nfor j in 1 2 3; do\n  echo b$j\ndone\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0 (stderr=%q)", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a1\na2\na3\nb1\nb2\nb3\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
