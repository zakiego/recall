package apps_test

import (
	"testing"

	"github.com/zakiego/recall/apps/server/internal/testkit"
)

func runMain(m *testing.M) int {
	testkit.Setup()
	return m.Run()
}
