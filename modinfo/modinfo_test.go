package modinfo

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseStrict(t *testing.T) {
	info, err := Parse([]byte(`{"modid":"smithingplus","name":"Smithing Plus","version":"2.4.1","dependencies":{"game":"1.19.0"}}`))
	if err != nil {
		t.Fatalf("Parse returned an error: %v", err)
	}
	if info.ModID != "smithingplus" || info.Name != "Smithing Plus" || info.Version != "2.4.1" {
		t.Fatalf("unexpected info: %#v", info)
	}
	if got := info.Dependencies["game"]; got != "1.19.0" {
		t.Fatalf("unexpected dependency: %q", got)
	}
}

func TestParseNormalizesMissingDependencies(t *testing.T) {
	info, err := Parse([]byte(`{"modid":"simple"}`))
	if err != nil {
		t.Fatalf("Parse returned an error: %v", err)
	}
	if info.Dependencies == nil {
		t.Fatal("Dependencies must not be nil")
	}
}

func TestParseStripsByteOrderMark(t *testing.T) {
	data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"modid":"bommod","name":"BOM Mod"}`)...)
	info, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse returned an error: %v", err)
	}
	if info.ModID != "bommod" || info.Name != "BOM Mod" {
		t.Fatalf("unexpected info: %#v", info)
	}
}

func TestParseLenientJSON(t *testing.T) {
	data := []byte(`{
		"modid": "lenient",
		"name": "Lenient Mod",
		"version": "1.0.0",
		"dependencies": {
			"game": "1.19.0",
			"libmod": ">=1.2.0",
		},
		"description": "trailing comma above",
	}`)
	info, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse returned an error: %v", err)
	}
	if info.ModID != "lenient" || info.Name != "Lenient Mod" || info.Version != "1.0.0" {
		t.Fatalf("unexpected info: %#v", info)
	}
	if info.Dependencies["game"] != "1.19.0" || info.Dependencies["libmod"] != ">=1.2.0" {
		t.Fatalf("unexpected dependencies: %#v", info.Dependencies)
	}
}

func TestParseLenientKeepsStringDependenciesOnly(t *testing.T) {
	data := []byte(`{
		"modid": "weird",
		"dependencies": {
			"scalar": "1.0.0",
			"object": {"modid": "nested"},
			"array": ["a", "b"],
			"number": 3
		},
	}`)
	info, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse returned an error: %v", err)
	}
	if info.ModID != "weird" {
		t.Fatalf("unexpected info: %#v", info)
	}
	if len(info.Dependencies) != 2 || info.Dependencies["scalar"] != "1.0.0" || info.Dependencies["number"] != "3" {
		t.Fatalf("unexpected dependencies: %#v", info.Dependencies)
	}
}

func TestParseNumericModIDFallsBackLeniently(t *testing.T) {
	info, err := Parse([]byte(`{"modid": 12345, "name": "Numeric"}`))
	if err != nil {
		t.Fatalf("Parse returned an error: %v", err)
	}
	if info.ModID != "12345" || info.Name != "Numeric" {
		t.Fatalf("unexpected info: %#v", info)
	}
}

func TestParseRejectsUnusableContent(t *testing.T) {
	if _, err := Parse([]byte(`not json at all`)); err == nil {
		t.Fatal("expected an error for unusable content")
	}
}

func writeArchive(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mod.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	return path
}

func TestReadArchive(t *testing.T) {
	path := writeArchive(t, map[string]string{
		"modinfo.json": `{"modid":"smithingplus","name":"Smithing Plus","version":"2.4.1"}`,
	})
	info, err := ReadArchive(path)
	if err != nil {
		t.Fatalf("ReadArchive returned an error: %v", err)
	}
	if info.ModID != "smithingplus" || info.Name != "Smithing Plus" || info.Version != "2.4.1" {
		t.Fatalf("unexpected info: %#v", info)
	}
}

func TestReadArchiveFindsNestedModInfo(t *testing.T) {
	path := writeArchive(t, map[string]string{
		"assets/mods/deep/modinfo.json": `{"modid":"deep"}`,
	})
	info, err := ReadArchive(path)
	if err != nil {
		t.Fatalf("ReadArchive returned an error: %v", err)
	}
	if info.ModID != "deep" {
		t.Fatalf("unexpected info: %#v", info)
	}
}

func TestReadArchivePrefersRootModInfo(t *testing.T) {
	path := writeArchive(t, map[string]string{
		"modinfo.json":             `{"modid":"rootmod"}`,
		"assets/deep/modinfo.json": `{"modid":"deepmod"}`,
	})
	info, err := ReadArchive(path)
	if err != nil {
		t.Fatalf("ReadArchive returned an error: %v", err)
	}
	if info.ModID != "rootmod" {
		t.Fatalf("expected the root modinfo.json to win, got %#v", info)
	}
}

func TestReadArchiveNoModInfo(t *testing.T) {
	path := writeArchive(t, map[string]string{"readme.txt": "nothing to see"})
	info, err := ReadArchive(path)
	if !errors.Is(err, ErrNoModInfo) {
		t.Fatalf("expected ErrNoModInfo, got %#v, %v", info, err)
	}
}

func TestReadArchiveNotAZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mod.cs")
	if err := os.WriteFile(path, []byte("// not a zip archive"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := ReadArchive(path); !errors.Is(err, ErrNotAnArchive) {
		t.Fatalf("expected ErrNotAnArchive, got %v", err)
	}
}

func TestReadArchiveRejectsOversizedEntry(t *testing.T) {
	big := strings.Repeat("x", MaxBytes+1)
	path := writeArchive(t, map[string]string{
		"modinfo.json": `{"modid":"big","description":"` + big + `"}`,
	})
	if _, err := ReadArchive(path); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
}

func TestReadArchiveInvalidContent(t *testing.T) {
	path := writeArchive(t, map[string]string{"modinfo.json": `not json`})
	if _, err := ReadArchive(path); err == nil {
		t.Fatal("expected an error for invalid modinfo.json")
	}
}
