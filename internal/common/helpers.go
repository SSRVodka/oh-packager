package common

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/SSRVodka/oh-packager/pkg/meta"
	"github.com/blang/semver/v4"
	"github.com/mholt/archiver/v3"
)

// Constraint represents a single operator constraint on a version.
type Constraint struct {
	Op  string // one of ">=", "<=", ">", "<", "==", "" (empty = any)
	Ver string // version string
}

var (
	// Regex to match valid operators: >=, <=, >, <, ==
	validOps = map[string]bool{
		">=": true,
		"<=": true,
		">":  true,
		"<":  true,
		"==": true,
	}

	// Pattern to extract name, operator, and version from dependency string
	// Matches: name followed by optional (operator + version)
	//  ^([^\s<>=]+)       -> capture the name: one or more chars that are not whitespace or <,>,=
	//  \s*                -> optional spaces
	//  (>=|<=|>|<|==)     -> capturing group for an operator (must be contiguous)
	//  \s*(.*)$            -> optional spaces then the rest is the version (capture)
	depPattern = regexp.MustCompile(`^([^\s<>=]+)\s*(>=|<=|>|<|==)?\s*(.*)$`)
)

// Get the absolute path in this system
func GetAbsolutePath(path string) (string, error) {
	return filepath.Abs(path)
}

func GetOhosArchDepLibDirRelPath(arch string) (string, error) {
	var err error
	arch, err = MapArchStr(arch)
	return "lib/" + arch + "-linux-ohos", err
}

func GetOhosSharedDirRelPath() string {
	return "share"
}

func GetInvalidPkgNameCharsInStr() string {
	return ">< =&|;,"
}

func GetDepsSepCharsInStr() string {
	return ",;&|"
}

func GetPostInstScriptName() string {
	return "postinst"
}

// Check directory exists
func IsDirExists(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false // Error occurred (e.g., path does not exist or permission issue)
	}
	return fileInfo.IsDir()
}

// Check file exists
func IsFileExists(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !fileInfo.IsDir()
}

// Check HTTP/HTTPS URL
func IsValidHttpUrl(urlStr string) bool {
	parsedURL, err := url.ParseRequestURI(urlStr)
	if err != nil {
		return false
	}
	// Check if the scheme is either "http" or "https"
	isHttpOrHttps := parsedURL.Scheme == "http" || parsedURL.Scheme == "https"
	// Ensure the URL has a non-empty host (e.g., "example.com" in "https://example.com")
	hasValidHost := parsedURL.Host != ""
	return isHttpOrHttps && hasValidHost
}

func IsArchDependentLib(path string) bool {
	basename := filepath.Base(path)
	return strings.HasSuffix(basename, ".so") || strings.HasSuffix(basename, ".a")
}

func IsPkgPath(path string) bool {
	basename := filepath.Base(path)
	if !strings.HasSuffix(basename, ".pkg") {
		return false
	}
	if !IsFileExists(path) {
		return false
	}
	return true
}

func isValidPkg(path string) bool {
	// simple impl
	return IsPkgPath(path)
}

func ExecuteShell(scriptPath string, args ...string) (string, error) {
	cmd := exec.Command(scriptPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("error while executing shell '%s': %v, output: %s", scriptPath, err, string(output))
	}

	return string(output), nil
}

// ASSUME: pkgVersion & pkgArch & pkgAPI doesn't contains '-'

func GenPkgFileName(pkgName, pkgVersion, pkgArch, pkgAPI string) string {
	return fmt.Sprintf("%s-%s-%s-api%s.pkg", pkgName, pkgVersion, pkgArch, pkgAPI)
}
func GenPkgManifestName(pkgName, pkgVersion, pkgArch, pkgAPI string) string {
	return fmt.Sprintf("%s-%s-%s-api%s.json", pkgName, pkgVersion, pkgArch, pkgAPI)
}

/** @return (pkgName, pkgVersion, pkgArch, pkgAPI, error) */
func ParsePkgNameFromPath(path string) (string, string, string, string, error) {
	basename := filepath.Base(path)
	ext := filepath.Ext(path)
	baseWithoutExt := strings.TrimSuffix(basename, ext)
	tokens := strings.Split(baseWithoutExt, "-")
	tl := len(tokens)
	if len(tokens) < 4 {
		return "", "", "", "", fmt.Errorf("invalid package name: '%s'", basename)
	}
	pkgAPI := strings.TrimPrefix(tokens[tl-1], "api")
	pkgArch := tokens[tl-2]
	pkgVersion := tokens[tl-3]
	tokens = tokens[:tl-3]
	return strings.Join(tokens, "-"), pkgVersion, pkgArch, pkgAPI, nil
}

// ParseDep parses dependency tokens like:
//
//	"libfoo >= 1.2.3"
//	"libbar == 1.0.0"
//	"openssl"
//	"libfoo<1.0"
//
// Returns (name, constraint, error).
func ParseDep(dep string) (string, Constraint, error) {
	dep = strings.TrimSpace(dep)
	if dep == "" {
		return "", Constraint{}, fmt.Errorf("empty dependency string")
	}

	matches := depPattern.FindStringSubmatch(dep)
	if matches == nil {
		return "", Constraint{}, fmt.Errorf("invalid dependency format: %s", dep)
	}

	name := strings.TrimSpace(matches[1])
	op := matches[2]
	verStr := strings.TrimSpace(matches[3])

	// Case 1: No operator - just a package name
	if op == "" && verStr == "" {
		return name, Constraint{Op: "", Ver: ""}, nil
	}

	// Case 2: Operator without version
	if op != "" && verStr == "" {
		return "", Constraint{}, fmt.Errorf("operator '%s' specified but no version provided", op)
	}

	// Case 3: Version without operator (invalid)
	if op == "" && verStr != "" {
		return "", Constraint{}, fmt.Errorf("version '%s' specified but no operator provided", verStr)
	}

	// Case 4: Both operator and version present
	// Remove quotes from version if present
	verStr = strings.Trim(verStr, `"'`)

	// Validate the operator (this catches cases like "< =" which would be parsed as "<" with ver "= 0.0.1")
	if !validOps[op] {
		return "", Constraint{}, fmt.Errorf("invalid operator: %s", op)
	}

	// Validate semantic version using semver library
	_, err := semver.ParseTolerant(verStr)
	if err != nil {
		return "", Constraint{}, fmt.Errorf("invalid semantic version '%s': %w", verStr, err)
	}

	return name, Constraint{Op: op, Ver: verStr}, nil
}

func JoinURL(base, rel string) string {
	base = strings.TrimRight(base, "/")
	rel = strings.TrimLeft(rel, "/")
	return base + "/" + rel
}

// Map arch to universal format
func MapArchStr(arch string) (string, error) {
	arch = strings.ToLower(arch)
	switch arch {
	case "arm64", "aarch64", "armv8a", "arm64v8a", "arm64-v8a":
		return "aarch64", nil
	case "arm", "armeabi-v7a", "armv7-a", "armv7a":
		return "arm", nil
	case "x86_64", "amd64":
		return "x86_64", nil
	default:
		return "", fmt.Errorf("unsupported architecture: '%s'", arch)
	}
}

// ComputeSHA256 computes sha256 checksum of a file.
func ComputeSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// TarGzDir creates a tar.gz archive from srcDir and writes to outPath.
func TarGzDir(srcDir, outPath string, includedPaths []string, excludedNames []string) error {
	// validate source
	info, err := os.Stat(srcDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("srcDir must be a directory")
	}

	dir := filepath.Dir(outPath)
	baseName := filepath.Base(outPath)
	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)
	newTarGzName := nameWithoutExt + ".tar.gz"
	newTarGzPath := filepath.Join(dir, newTarGzName)

	// // archiver.Archive will create a tar.gz when dest ends with .tar.gz or .tgz;
	// // it will include the srcDir as a top-level entry (preserves layout).
	// if err := archiver.Archive([]string{srcDir}, newTarGzPath); err != nil {
	// 	return err
	// }

	// NOT preserving layout (without srcDir)
	// Collect all immediate entries inside srcDir (files + subdirs)
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	var paths []string
	var excludedSet map[string]struct{} = sliceToSet(excludedNames)
	for _, e := range entries {
		if _, isExcluded := excludedSet[e.Name()]; isExcluded {
			continue
		}
		paths = append(paths, filepath.Join(srcDir, e.Name()))
	}
	paths = append(paths, includedPaths...)
	// Use archiver.DefaultTarGz to force tar.gz format regardless of extension.
	if err := archiver.DefaultTarGz.Archive(paths, newTarGzPath); err != nil {
		return err
	}

	// rename
	if err := os.Rename(newTarGzPath, outPath); err != nil {
		return fmt.Errorf("failed to rename '%s' to pkg ext", newTarGzPath)
	}
	return nil
}

// ReadManifest reads a manifest JSON from path into Manifest.
func ReadManifest(path string) (*meta.Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m meta.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// WriteManifest writes the manifest to path.
func WriteManifest(path string, m *meta.Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// EnsureRepoDirs creates the standard repo layout under basePath.
func EnsureRepoDirs(basePath string) error {
	dirs := []string{
		filepath.Join(basePath, "channels"),
		filepath.Join(basePath, "public_keys"),
		filepath.Join(basePath, "signatures"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// EnsureChannelDirs ensures a channel layout (channel/pkgs).
func EnsureChannelDirs(basePath, channel string) (string, error) {
	channelPath := filepath.Join(basePath, "channels", channel)
	pkgs := filepath.Join(channelPath, "pkgs")
	if err := os.MkdirAll(pkgs, 0o755); err != nil {
		return "", err
	}
	return channelPath, nil
}

// TODO: use different channel for difference arch
// DeployPackage copies .pkg and .json manifest into channel pkgs and regenerates index.
func DeployPackage(basePath, channel, pkgFile, manifestFile string) error {
	if pkgFile == "" || manifestFile == "" {
		return errors.New("pkgFile and manifestFile are required")
	}
	chPath, err := EnsureChannelDirs(basePath, channel)
	if err != nil {
		return err
	}
	pkgsDir := filepath.Join(chPath, "pkgs")

	// read manifest
	manifest, err := ReadManifest(manifestFile)
	if err != nil {
		return err
	}

	// validate package
	if !isValidPkg(pkgFile) {
		return fmt.Errorf("not a valid package file: %s", pkgFile)
	}

	// destination names
	pkgBase := GenPkgFileName(manifest.Name, manifest.Version, manifest.Arch, manifest.OhosApi)
	manifestBase := GenPkgManifestName(manifest.Name, manifest.Version, manifest.Arch, manifest.OhosApi)

	dstPkg := filepath.Join(pkgsDir, pkgBase)
	dstManifest := filepath.Join(pkgsDir, manifestBase)

	// copy files
	if err := copyFile(pkgFile, dstPkg); err != nil {
		return err
	}
	// recompute size and sha256 from file to be robust
	sz, err := fileSize(dstPkg)
	if err != nil {
		return err
	}
	sum, err := ComputeSHA256(dstPkg)
	if err != nil {
		return err
	}
	manifest.Size = sz
	manifest.SHA256 = sum
	// update manifest URL to a path relative to repo root (client can choose full URL)
	manifest.URL = fmt.Sprintf("channels/%s/pkgs/%s", channel, pkgBase)
	if err := WriteManifest(dstManifest, manifest); err != nil {
		return err
	}

	// regenerate index.json
	if err := regenerateIndex(basePath, channel); err != nil {
		return err
	}
	return nil
}

func regenerateIndex(basePath, channel string) error {
	chPath := filepath.Join(basePath, "channels", channel)
	pkgsDir := filepath.Join(chPath, "pkgs")
	entries := []meta.IndexEntry{}

	err := filepath.WalkDir(pkgsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		// manifest file
		m, err := ReadManifest(path)
		if err != nil {
			return err
		}
		base := filepath.Base(path)
		// find pkg basename (replace .json with .pkg)
		pkgName := base[:len(base)-len(".json")] + ".pkg"
		url := fmt.Sprintf("channels/%s/pkgs/%s", channel, pkgName)
		entries = append(entries, meta.IndexEntry{
			Name:     m.Name,
			Version:  m.Version,
			Arch:     m.Arch,
			OhosApi:  m.OhosApi,
			URL:      url,
			SHA256:   m.SHA256,
			Size:     m.Size,
			Manifest: fmt.Sprintf("channels/%s/pkgs/%s", channel, filepath.Base(path)),
			Depends:  m.Depends,
		})
		return nil
	})
	if err != nil {
		return err
	}
	idx := meta.Index{
		Repo:      filepath.Base(basePath),
		Channel:   channel,
		Generated: time.Now().UTC(),
		Packages:  entries,
	}
	out, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	indexPath := filepath.Join(chPath, "index.json")
	return os.WriteFile(indexPath, out, 0o644)
}

// copyFile copies src to dst (overwrites).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// Get source file info to preserve permissions
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// Create destination file with same permissions
	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	// Copy content
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	// Ensure contents are flushed to disk
	return out.Sync()
}

func fileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func sliceToSet[T int | string](slice []T) map[T]struct{} {
	m := make(map[T]struct{}, len(slice))
	for _, s := range slice {
		m[s] = struct{}{}
	}
	return m
}
