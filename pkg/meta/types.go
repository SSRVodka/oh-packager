package meta

import (
	"fmt"
	"strings"
	"time"
)

// Manifest describes a package.
type Manifest struct {
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Arch          string   `json:"arch"`
	OhosApi       string   `json:"ohos_api"`
	Format        int      `json:"format_version"`
	Summary       string   `json:"summary,omitempty"`
	Description   string   `json:"description,omitempty"`
	Maintainer    string   `json:"maintainer,omitempty"`
	License       string   `json:"license,omitempty"`
	Size          int64    `json:"size"`
	SHA256        string   `json:"sha256"`
	URL           string   `json:"url,omitempty"`
	Provides      []string `json:"provides,omitempty"`
	Depends       []string `json:"depends,omitempty"`
	Relocatable   bool     `json:"relocatable,omitempty"`
	InstallPrefix string   `json:"install_prefix,omitempty"`
}

// Index contains package entries for a channel.
type Index struct {
	Repo      string       `json:"repo,omitempty"`
	Channel   string       `json:"channel,omitempty"`
	Generated time.Time    `json:"generated"`
	Packages  []IndexEntry `json:"packages"`
}

type IndexEntry struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Arch     string   `json:"arch"`
	OhosApi  string   `json:"ohos_api"`
	URL      string   `json:"url"`
	SHA256   string   `json:"sha256"`
	Size     int64    `json:"size"`
	Manifest string   `json:"manifest,omitempty"`
	Depends  []string `json:"depends,omitempty"`
}

type OhosSdkInfo struct {
	// ONLY need this one for now
	ApiVersion string `json:"apiVersion"`
}

// pkgs VERSION
// PkgSourceInfo represents the parsed information from VERSION file in pkgs patch repo
type PackageInfo struct {
	Name         string
	Version      string
	Depends      []string
	BuildDepends []string
}

// ParseVersionLine parses a single line from VERSION file
// Format: <name> <version> [dependencies] [build_dependencies]
// Returns (PackageInfo, error)
func ParseVersionLine(line string) (*PackageInfo, error) {
	// Remove comments
	if idx := strings.Index(line, "#"); idx >= 0 {
		line = line[:idx]
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil // Empty line
	}

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil, fmt.Errorf("invalid VERSION line: need at least name and version")
	}

	info := &PackageInfo{
		Name:         fields[0],
		Version:      fields[1],
		Depends:      []string{},
		BuildDepends: []string{},
	}

	if len(fields) > 2 {
		// Parse dependencies (3rd field)
		deps := strings.Split(fields[2], ",")
		for _, dep := range deps {
			dep = strings.TrimSpace(dep)
			if dep != "" {
				info.Depends = append(info.Depends, dep)
			}
		}
	}

	if len(fields) > 3 {
		// Parse build dependencies (4th field)
		buildDeps := strings.Split(fields[3], ",")
		for _, dep := range buildDeps {
			dep = strings.TrimSpace(dep)
			if dep != "" {
				info.BuildDepends = append(info.BuildDepends, dep)
			}
		}
	}

	return info, nil
}
