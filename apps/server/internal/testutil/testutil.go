// Package testutil has helpers shared by the tests: paths into testdata and
// golden loading.
package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Root is the apps/server folder.
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// Testdata joins parts under apps/server/testdata.
func Testdata(parts ...string) string {
	return filepath.Join(append([]string{Root(), "testdata"}, parts...)...)
}

// AppDir is apps/app (the TS build, the spec while it exists).
func AppDir(parts ...string) string {
	return filepath.Join(append([]string{Root(), "..", "app"}, parts...)...)
}

// LoadJSON reads testdata/<name> into v.
func LoadJSON(t testing.TB, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile(Testdata(name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// ModelsDir is apps/app/data/models unless MODELS_DIR says otherwise.
func ModelsDir() string {
	if d := os.Getenv("MODELS_DIR"); d != "" {
		return d
	}
	return AppDir("data", "models")
}
