package runtime

import (
	"reflect"
	"testing"
)

func TestCompatibilityCorpusIsDeterministicAcrossFreshSessions(t *testing.T) {
	fixtures := loadExecutionFixtures(t, "compatibility/*.json")

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()

			first := canonicalizeResult(normalizeResult(runExecutionFixture(t, &fixture)))
			second := canonicalizeResult(normalizeResult(runExecutionFixture(t, &fixture)))

			if !reflect.DeepEqual(first, second) {
				t.Fatalf("determinism mismatch\n\ngot:\n%s\n\nwant:\n%s", mustJSON(t, second), mustJSON(t, first))
			}
		})
	}
}

func TestWorkflowScenariosAreDeterministicAcrossFreshSessions(t *testing.T) {
	testCases := []struct {
		name string
		run  func(*testing.T) []normalizedExecutionResult
	}{
		{
			name: "codebase-exploration",
			run:  runCodebaseExplorationWorkflow,
		},
		{
			name: "refactor-preparation",
			run:  runRefactorPreparationWorkflow,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			first := tt.run(t)
			second := tt.run(t)
			for i := range first {
				first[i] = canonicalizeResult(first[i])
			}
			for i := range second {
				second[i] = canonicalizeResult(second[i])
			}

			if !reflect.DeepEqual(first, second) {
				t.Fatalf("workflow determinism mismatch\n\ngot:\n%s\n\nwant:\n%s", mustJSON(t, second), mustJSON(t, first))
			}
		})
	}
}
