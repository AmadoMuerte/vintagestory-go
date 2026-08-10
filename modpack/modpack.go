// Package modpack analyzes installed Vintage Story mods and available catalog updates.
package modpack

import (
	"context"
	"golang.org/x/mod/semver"
	"strings"
)

type Build struct {
	GameVersion string
	Mods        []ModInstall
}
type ModInstall struct {
	ModID, Name, Version, FileName string
	Managed, Enabled               bool
	Dependencies                   []string
}
type Dependency struct{ ModID, Name, Requirement string }
type ModStatus string

const (
	StatusUpToDate        ModStatus = "up_to_date"
	StatusUpdateAvailable ModStatus = "update_available"
	StatusNotUpdatable    ModStatus = "not_updatable"
	StatusUnknown         ModStatus = "unknown"
)

type NotUpdatableReason string

const (
	ReasonNone         NotUpdatableReason = ""
	ReasonLocalMod     NotUpdatableReason = "local_mod"
	ReasonNotInCatalog NotUpdatableReason = "not_in_catalog"
	ReasonCatalogError NotUpdatableReason = "catalog_error"
)

type ModUpdate struct {
	ModID, Name, InstalledVersion, TargetVersionID, TargetVersion string
	Status                                                        ModStatus
	Reason                                                        NotUpdatableReason
	Changelog                                                     string
	Compatible, Prerelease                                        bool
	AddedDeps, RemovedDeps                                        []Dependency
}
type Summary struct{ TotalMods, UpToDate, UpdatesAvailable, NotUpdatableLocal, NotUpdatableAbsent, NotUpdatableCatalogError, Incompatible int }
type Report struct {
	Build   Build
	Mods    []ModUpdate
	Summary Summary
}

// Catalog is the read-only source of metadata used by Analyze. Unknown mods must return empty ModInfo.
type Catalog interface {
	Get(context.Context, string) (ModInfo, error)
}
type ModInfo struct {
	ID, LatestVersion string
	Versions          []ModVersion
}
type ModVersion struct {
	ID, Version, ReleaseType string
	GameVersions             []string
	Changelog                string
	Dependencies             []Dependency
}

// Analyze reports available updates without modifying the build.
func Analyze(ctx context.Context, build Build, catalog Catalog) (Report, error) {
	report := Report{Build: build, Mods: make([]ModUpdate, 0, len(build.Mods))}
	installed := map[string]struct{}{}
	for _, m := range build.Mods {
		if m.ModID != "" {
			installed[m.ModID] = struct{}{}
		}
	}
	for _, m := range build.Mods {
		if e := ctx.Err(); e != nil {
			return Report{}, e
		}
		report.Mods = append(report.Mods, analyzeMod(ctx, m, build.GameVersion, installed, catalog))
	}
	report.Summary = summarize(report.Mods)
	return report, nil
}
func analyzeMod(ctx context.Context, m ModInstall, game string, installed map[string]struct{}, catalog Catalog) ModUpdate {
	r := ModUpdate{ModID: m.ModID, Name: m.Name, InstalledVersion: m.Version, Status: StatusUnknown}
	if !m.Managed || m.ModID == "" {
		r.Status = StatusNotUpdatable
		r.Reason = ReasonLocalMod
		return r
	}
	info, e := catalog.Get(ctx, m.ModID)
	if e != nil {
		r.Status = StatusNotUpdatable
		r.Reason = ReasonCatalogError
		return r
	}
	if info.ID == "" {
		r.Status = StatusNotUpdatable
		r.Reason = ReasonNotInCatalog
		return r
	}
	target, ok := selectTargetVersion(info, m.Version)
	if !ok {
		r.Status = StatusUpToDate
		return r
	}
	r.Status = StatusUpdateAvailable
	r.TargetVersionID, r.TargetVersion, r.Changelog = target.ID, target.Version, target.Changelog
	r.Compatible = supportsGameVersion(target.GameVersions, game)
	r.Prerelease = !isStableRelease(target.ReleaseType)
	r.AddedDeps, r.RemovedDeps = dependencyDiff(m.Dependencies, target.Dependencies, installed)
	return r
}
func selectTargetVersion(info ModInfo, installed string) (ModVersion, bool) {
	if info.LatestVersion != "" {
		if VersionEquals(info.LatestVersion, installed) {
			return ModVersion{}, false
		}
		if v, ok := findVersion(info.Versions, info.LatestVersion); ok {
			return v, true
		}
	}
	if v, ok := newestVersion(info.Versions, true); ok {
		if VersionEquals(v.Version, installed) {
			return ModVersion{}, false
		}
		return v, true
	}
	v, ok := newestVersion(info.Versions, false)
	return v, ok && !VersionEquals(v.Version, installed)
}
func findVersion(v []ModVersion, version string) (ModVersion, bool) {
	for _, x := range v {
		if VersionEquals(x.Version, version) {
			return x, true
		}
	}
	return ModVersion{}, false
}
func newestVersion(v []ModVersion, stable bool) (ModVersion, bool) {
	var best ModVersion
	found := false
	for _, x := range v {
		if stable && !isStableRelease(x.ReleaseType) {
			continue
		}
		if !found || CompareVersions(x.Version, best.Version) > 0 {
			best, found = x, true
		}
	}
	return best, found
}
func isStableRelease(v string) bool { return v == "" || strings.EqualFold(v, "stable") }
func summarize(mods []ModUpdate) Summary {
	s := Summary{TotalMods: len(mods)}
	for _, m := range mods {
		switch m.Status {
		case StatusUpToDate:
			s.UpToDate++
		case StatusUpdateAvailable:
			s.UpdatesAvailable++
			if !m.Compatible {
				s.Incompatible++
			}
		case StatusNotUpdatable:
			switch m.Reason {
			case ReasonLocalMod:
				s.NotUpdatableLocal++
			case ReasonNotInCatalog:
				s.NotUpdatableAbsent++
			case ReasonCatalogError:
				s.NotUpdatableCatalogError++
			}
		}
	}
	return s
}
func dependencyDiff(old []string, target []Dependency, installed map[string]struct{}) (added, removed []Dependency) {
	declared := map[string]struct{}{}
	for _, x := range old {
		declared[x] = struct{}{}
	}
	for _, d := range target {
		if isBuiltInDependency(d.ModID) {
			continue
		}
		if _, ok := installed[d.ModID]; ok {
			continue
		}
		if _, ok := declared[d.ModID]; !ok {
			added = append(added, d)
		}
	}
	for _, id := range old {
		if !isBuiltInDependency(id) && !stillRequired(id, target) {
			if _, ok := installed[id]; ok {
				removed = append(removed, Dependency{ModID: id})
			}
		}
	}
	return
}
func stillRequired(id string, v []Dependency) bool {
	for _, d := range v {
		if d.ModID == id {
			return true
		}
	}
	return false
}
func isBuiltInDependency(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "game", "survival", "creative":
		return true
	}
	return false
}

// VersionEquals compares semantic versions when possible and otherwise compares case-insensitive strings.
func VersionEquals(a, b string) bool {
	if a == "" || b == "" {
		return strings.EqualFold(a, b)
	}
	x, y := normalizeSemver(a), normalizeSemver(b)
	if x != "" && y != "" {
		return x == y
	}
	return strings.EqualFold(a, b)
}

// CompareVersions returns whether left is older than, equal to, or newer than right.
func CompareVersions(a, b string) int {
	x, y := normalizeSemver(a), normalizeSemver(b)
	switch {
	case x != "" && y != "":
		return semver.Compare(x, y)
	case x != "":
		return 1
	case y != "":
		return -1
	}
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}
func supportsGameVersion(supported []string, requested string) bool {
	for _, v := range supported {
		if v == requested {
			return true
		}
		p := strings.Split(strings.TrimSpace(v), ".")
		if len(p) >= 2 && strings.HasPrefix(requested, p[0]+"."+p[1]+".") {
			return true
		}
	}
	return len(supported) == 0
}
func normalizeSemver(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(v), "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return semver.Canonical(v)
}
