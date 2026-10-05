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

func TestAnalyzeSelectsOnlyNewerTargets(t *testing.T) {
	tests := []struct {
		name, installed, latest, targetID, targetVersion string
		versions                                         []ModVersion
		status                                           ModStatus
		summary                                          Summary
	}{
		{"older latest has no upgrade", "2.0.0", "1.9.5", "", "", []ModVersion{{ID: "old", Version: "1.9.5"}, {ID: "equal", Version: "2.0.0"}}, StatusUpToDate, Summary{TotalMods: 1, UpToDate: 1}},
		{"fallback finds newer release", "2.0.0", "1.9.5", "new", "2.1.0", []ModVersion{{ID: "old", Version: "1.9.5"}, {ID: "equal", Version: "2.0.0"}, {ID: "new", Version: "2.1.0"}}, StatusUpdateAvailable, Summary{TotalMods: 1, UpdatesAvailable: 1}},
		{"newer latest", "2.0.0", "2.1.0", "new", "2.1.0", []ModVersion{{ID: "new", Version: "2.1.0"}}, StatusUpdateAvailable, Summary{TotalMods: 1, UpdatesAvailable: 1}},
		{"equal latest has no upgrade", "2.0.0", "2.0.0", "", "", []ModVersion{{ID: "equal", Version: "2.0.0"}}, StatusUpToDate, Summary{TotalMods: 1, UpToDate: 1}},
		{"major upgrade", "2.0.0", "3.0.0", "major", "3.0.0", []ModVersion{{ID: "major", Version: "3.0.0"}}, StatusUpdateAvailable, Summary{TotalMods: 1, UpdatesAvailable: 1}},
		{"equal latest falls through", "2.0.0", "2.0.0", "new", "2.1.0", []ModVersion{{ID: "equal", Version: "2.0.0"}, {ID: "new", Version: "2.1.0"}}, StatusUpdateAvailable, Summary{TotalMods: 1, UpdatesAvailable: 1}},
		{"unresolved latest cannot downgrade", "2.0.0", "missing", "", "", []ModVersion{{ID: "old", Version: "1.9.0"}, {ID: "equal", Version: "2.0.0"}}, StatusUpToDate, Summary{TotalMods: 1, UpToDate: 1}},
		{"newer prerelease fallback", "2.0.0", "missing", "pre", "2.1.0-beta.1", []ModVersion{{ID: "old", Version: "1.9.0"}, {ID: "equal", Version: "2.0.0"}, {ID: "pre", Version: "2.1.0-beta.1", ReleaseType: "beta"}}, StatusUpdateAvailable, Summary{TotalMods: 1, UpdatesAvailable: 1}},
		{"stable preferred over prerelease", "2.0.0", "missing", "stable", "2.1.0", []ModVersion{{ID: "pre", Version: "3.0.0-beta.1", ReleaseType: "beta"}, {ID: "stable", Version: "2.1.0", ReleaseType: "stable"}}, StatusUpdateAvailable, Summary{TotalMods: 1, UpdatesAvailable: 1}},
		{"numeric ordering", "2.9.0", "missing", "new", "2.10.0", []ModVersion{{ID: "old", Version: "2.9.1"}, {ID: "new", Version: "2.10.0"}}, StatusUpdateAvailable, Summary{TotalMods: 1, UpdatesAvailable: 1}},
		{"semantic equality", "v2.0", "2.0.0+build", "", "", []ModVersion{{ID: "equal", Version: "2.0.0+build"}}, StatusUpToDate, Summary{TotalMods: 1, UpToDate: 1}},
		{"prerelease ordering", "2.0.0-beta.2", "missing", "new", "2.0.0-beta.10", []ModVersion{{ID: "old", Version: "2.0.0-beta.9", ReleaseType: "beta"}, {ID: "new", Version: "2.0.0-beta.10", ReleaseType: "beta"}}, StatusUpdateAvailable, Summary{TotalMods: 1, UpdatesAvailable: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := Analyze(context.Background(), Build{Mods: []ModInstall{{ModID: "mod", Version: test.installed, Managed: true}}}, fakeCatalog{info: ModInfo{ID: "mod", LatestVersion: test.latest, Versions: test.versions}})
			if err != nil {
				t.Fatal(err)
			}
			got := report.Mods[0]
			if got.Status != test.status || got.TargetVersionID != test.targetID || got.TargetVersion != test.targetVersion || report.Summary != test.summary {
				t.Fatalf("got %#v, summary %#v; want status %q, target %q %q, summary %#v", got, report.Summary, test.status, test.targetID, test.targetVersion, test.summary)
			}
		})
	}
}
