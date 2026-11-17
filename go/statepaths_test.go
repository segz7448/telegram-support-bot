package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wr(t *testing.T, p, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStatePathsDefaultUnchanged(t *testing.T) {
	d := t.TempDir()
	s, dl, err := resolveStatePaths(d, "", "", func(string) {})
	if err != nil || s != filepath.Join(d, "state.json") || dl != filepath.Join(d, "dead_letter.jsonl") {
		t.Fatal(s, dl, err)
	}
}

func TestStatePathsMigrationPreservesLegacyOffset(t *testing.T) {
	d, x := t.TempDir(), t.TempDir()
	wr(t, filepath.Join(d, "state.json"), `{"offset":278262033}`)
	wr(t, filepath.Join(d, "dead_letter.jsonl"), "{\"update_id\":1}\n")
	ext := filepath.Join(x, "state", "state.json")
	extDead := filepath.Join(x, "state", "dead.jsonl")
	var logs []string
	s, dl, err := resolveStatePaths(d, ext, extDead, func(m string) { logs = append(logs, m) })
	if err != nil || s != ext || dl != extDead {
		t.Fatal(s, dl, err)
	}
	if off, err := loadOffset(ext); err != nil || off != 278262033 {
		t.Fatalf("offset %d %v", off, err)
	}
	if b, _ := os.ReadFile(extDead); !strings.Contains(string(b), "update_id") {
		t.Fatal("dead letters not copied")
	}
	// legacy untouched
	if off, _ := loadOffset(filepath.Join(d, "state.json")); off != 278262033 {
		t.Fatal("legacy changed")
	}
	// preserve current: external advances, legacy stale -> a second start keeps the external one
	saveOffset(ext, 278262100)
	if _, _, err := resolveStatePaths(d, ext, extDead, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if off, _ := loadOffset(ext); off != 278262100 {
		t.Fatalf("external regressed to %d", off)
	}
	// deployment folder deleted: external still there, legacy missing is fine
	os.RemoveAll(d)
	d2 := t.TempDir()
	if _, _, err := resolveStatePaths(d2, ext, extDead, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if off, _ := loadOffset(ext); off != 278262100 {
		t.Fatalf("offset lost: %d", off)
	}
	if n, err := showOffsetAt(d2, ext); err != nil || n != 278262100 {
		t.Fatal(n, err)
	}
}

func showOffsetAt(dir, ext string) (int, error) {
	os.Setenv("TG_STATE_FILE", ext)
	defer os.Unsetenv("TG_STATE_FILE")
	return showOffset(dir)
}

func TestStatePathsLegacyAheadWinsAndMalformedFailsClosed(t *testing.T) {
	d, x := t.TempDir(), t.TempDir()
	ext := filepath.Join(x, "state.json")
	wr(t, filepath.Join(d, "state.json"), `{"offset":500}`)
	wr(t, ext, `{"offset":100}`)
	if _, _, err := resolveStatePaths(d, ext, "", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if off, _ := loadOffset(ext); off != 500 {
		t.Fatalf("larger offset must win, got %d", off)
	}
	for _, bad := range []string{`not json`, `{}`, `{"offset":-5}`} {
		wr(t, ext, bad)
		if _, _, err := resolveStatePaths(d, ext, "", func(string) {}); err == nil {
			t.Fatalf("malformed external accepted: %s", bad)
		}
	}
	wr(t, ext, `{"offset":600}`)
	wr(t, filepath.Join(d, "state.json"), `garbage`)
	if _, _, err := resolveStatePaths(d, ext, "", func(string) {}); err == nil {
		t.Fatal("malformed legacy accepted")
	}
	if _, _, err := resolveStatePaths(d, "relative/state.json", "", func(string) {}); err == nil {
		t.Fatal("relative path accepted")
	}
	// neither exists: fresh start, no error
	if _, _, err := resolveStatePaths(t.TempDir(), filepath.Join(t.TempDir(), "s.json"), "", func(string) {}); err != nil {
		t.Fatal(err)
	}
	// the flow after resolving: the Runner reads and writes the external file
	d3, x3 := t.TempDir(), t.TempDir()
	wr(t, filepath.Join(d3, "state.json"), `{"offset":42}`)
	e3 := filepath.Join(x3, "s.json")
	sp, _, _ := resolveStatePaths(d3, e3, "", func(string) {})
	if off, _ := loadOffset(sp); off != 42 {
		t.Fatal(off)
	}
	saveOffset(sp, 43)
	if off, _ := loadOffset(filepath.Join(d3, "state.json")); off != 42 {
		t.Fatal("legacy was written")
	}
}
