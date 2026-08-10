package modpack

import (
	"context"
	"errors"
	"testing"
)

type fakeCatalog struct {
	info ModInfo
	err  error
}

func (f fakeCatalog) Get(context.Context, string) (ModInfo, error) { return f.info, f.err }
func TestAnalyzeUpdateDependencies(t *testing.T) {
	catalog := fakeCatalog{info: ModInfo{ID: "mod", LatestVersion: "1.2.0", Versions: []ModVersion{{ID: "r", Version: "1.2.0", ReleaseType: "stable", GameVersions: []string{"1.22"}, Dependencies: []Dependency{{ModID: "new"}}}}}}
	report, e := Analyze(context.Background(), Build{GameVersion: "1.22.1", Mods: []ModInstall{{ModID: "mod", Version: "1.0.0", Managed: true, Dependencies: []string{"old"}}, {ModID: "old", Managed: true}}}, catalog)
	if e != nil || report.Mods[0].Status != StatusUpdateAvailable || !report.Mods[0].Compatible || len(report.Mods[0].AddedDeps) != 1 || len(report.Mods[0].RemovedDeps) != 1 {
		t.Fatalf("%v %#v", e, report)
	}
}
func TestAnalyzeFailuresAndCompare(t *testing.T) {
	r, e := Analyze(context.Background(), Build{Mods: []ModInstall{{Name: "local"}, {ModID: "bad", Managed: true}}}, fakeCatalog{err: errors.New("x")})
	if e != nil || r.Summary.NotUpdatableLocal != 1 || r.Summary.NotUpdatableCatalogError != 1 {
		t.Fatalf("%v %#v", e, r)
	}
	if !VersionEquals("1.2.3", "v1.2.3") || CompareVersions("1.2.4", "1.2.3") <= 0 {
		t.Fatal("version comparison failed")
	}
}
