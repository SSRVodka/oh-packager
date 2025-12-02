package meta

import "time"

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
