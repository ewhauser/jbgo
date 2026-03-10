package runtime

import "testing"

func TestCompatibilityCorpus(t *testing.T) {
	fixtures := loadExecutionFixtures(t, "compatibility/*.json")

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()

			result := runExecutionFixture(t, &fixture)
			assertExecutionOutcome(t, normalizeResult(result), fixture.ExitCode, fixture.Stdout, fixture.Stderr)
		})
	}
}
