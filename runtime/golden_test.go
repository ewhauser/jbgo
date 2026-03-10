package runtime

import "testing"

func TestExecutionGoldens(t *testing.T) {
	fixtures := loadExecutionFixtures(t, "golden/*.json")

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			t.Parallel()

			result := runExecutionFixture(t, &fixture)
			assertNormalizedResult(t, normalizeResult(result), normalizedExecutionResult{
				ExitCode: fixture.ExitCode,
				Stdout:   fixture.Stdout,
				Stderr:   fixture.Stderr,
				Events:   fixture.Events,
			})
		})
	}
}
