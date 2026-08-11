// Package modinfo parses the modinfo.json metadata a Vintage Story mod
// declares inside its archive.
package modinfo

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
)

// MaxBytes is the largest modinfo.json content accepted by Parse and
// ReadArchive. A mod that exceeds it is treated as corrupt rather than
// trusted.
const MaxBytes = 1 << 20

// Info is the metadata a Vintage Story mod declares in modinfo.json.
type Info struct {
	ModID        string
	Name         string
	Version      string
	Dependencies map[string]string
}

var (
	// ErrNotAnArchive is returned by ReadArchive when the file is not a ZIP
	// archive and therefore cannot contain modinfo.json.
	ErrNotAnArchive = errors.New("not a zip archive")
	// ErrNoModInfo is returned by ReadArchive when the archive contains no
	// modinfo.json entry.
	ErrNoModInfo = errors.New("archive does not contain modinfo.json")
	// ErrTooLarge is returned when the modinfo.json entry is bigger than
	// MaxBytes.
	ErrTooLarge = errors.New("modinfo.json is unexpectedly large")
)

// Parse reads the modinfo.json content of a mod. It tolerates a UTF-8 byte
// order mark and lenient JSON such as trailing commas that the game accepts
// but the standard library rejects. When the strict parse fails, the core
// fields are extracted leniently; an error is returned only when nothing
// usable could be parsed at all.
func Parse(data []byte) (Info, error) {
	// Some mod packs save modinfo.json with a UTF-8 byte order mark, which the
	// standard library rejects even though the JSON itself is valid.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	info := Info{}
	strictErr := json.Unmarshal(data, &info)
	if strictErr == nil {
		if info.Dependencies == nil {
			info.Dependencies = map[string]string{}
		}
		return info, nil
	}
	// A few mods publish modinfo.json with lenient JSON, such as trailing
	// commas, that the game accepts but the standard library rejects. Keep the
	// core fields so the mod can still be matched and installed, and preserve
	// the string dependencies that are parseable.
	info = Info{Dependencies: map[string]string{}}
	info.ModID = strings.TrimSpace(gjson.GetBytes(data, "modid").String())
	info.Name = strings.TrimSpace(gjson.GetBytes(data, "name").String())
	info.Version = strings.TrimSpace(gjson.GetBytes(data, "version").String())
	if info.ModID == "" && info.Name == "" && info.Version == "" {
		return Info{}, strictErr
	}
	if dependencies := gjson.GetBytes(data, "dependencies"); dependencies.IsObject() {
		dependencies.ForEach(func(key, value gjson.Result) bool {
			if !value.IsObject() && !value.IsArray() {
				info.Dependencies[key.String()] = value.String()
			}
			return true
		})
	}
	return info, nil
}

// ReadArchive extracts modinfo.json from a Vintage Story mod archive. An empty
// Info with a nil error is returned when the archive contains no
// modinfo.json. ErrNotAnArchive is returned for files that are not ZIP
// archives, and ErrTooLarge when the entry exceeds MaxBytes.
func ReadArchive(path string) (Info, error) {
	reader, err := zip.OpenReader(path)
	if errors.Is(err, zip.ErrFormat) {
		return Info{}, ErrNotAnArchive
	}
	if err != nil {
		return Info{}, fmt.Errorf("open mod archive: %w", err)
	}
	defer reader.Close()

	var modInfoFile *zip.File
	for _, file := range reader.File {
		name := strings.TrimPrefix(filepath.ToSlash(file.Name), "./")
		if strings.EqualFold(name, "modinfo.json") {
			modInfoFile = file
			break
		}
		if modInfoFile == nil && strings.EqualFold(filepath.Base(name), "modinfo.json") {
			modInfoFile = file
		}
	}
	if modInfoFile == nil {
		return Info{}, ErrNoModInfo
	}
	if modInfoFile.UncompressedSize64 > MaxBytes {
		return Info{}, ErrTooLarge
	}

	file, err := modInfoFile.Open()
	if err != nil {
		return Info{}, fmt.Errorf("open modinfo.json: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
	if err != nil {
		return Info{}, fmt.Errorf("read modinfo.json: %w", err)
	}
	if len(data) > MaxBytes {
		return Info{}, ErrTooLarge
	}
	info, err := Parse(data)
	if err != nil {
		return Info{}, fmt.Errorf("parse modinfo.json: %w", err)
	}
	return info, nil
}
