package common

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/SSRVodka/oh-packager/pkg/config"
	"github.com/SSRVodka/oh-packager/pkg/meta"
	"github.com/blang/semver/v4"
	"github.com/mholt/archiver/v3"
)

const DEFAULT_CONFIG_DIR string = "oh_pkgmgr"

// Constraint represents a single operator constraint on a version.
type Constraint struct {
	Op  string // one of ">=", "<=", ">", "<", "==", "" (empty = any)
	Ver string // version string
}

// parseDep parses a dependency token like:
//
//	"libfoo >= 1.2.3"
//	"libbar == 1.0.0"
//	"openssl"
//
// returns name, Constraint (op=="" if none)
func ParseDep(dep string) (string, Constraint) {
	dep = strings.TrimSpace(dep)
	// split by first space
	parts := strings.Fields(dep)
	if len(parts) == 1 {
		return parts[0], Constraint{Op: "", Ver: ""}
	}
	// expected: name op version
	name := parts[0]
	if len(parts) >= 3 {
		op := parts[1]
		ver := parts[2]
		// strip quotes if any
		ver = strings.Trim(ver, `"'`)
		return name, Constraint{Op: op, Ver: ver}
	}
	// fallback: treat as name only
	return dep, Constraint{Op: "", Ver: ""}
}

// satisfies checks if version satisfies all constraints
func SatisfiesConstraints(version string, constraints []Constraint) bool {
	if len(constraints) == 0 {
		return true
	}
	v, err := semver.ParseTolerant(version)
	if err != nil {
		// if we can't parse, be conservative and return false
		return false
	}
	for _, c := range constraints {
		if c.Op == "" {
			continue
		}
		cv, err := semver.ParseTolerant(c.Ver)
		if err != nil {
			return false
		}
		switch c.Op {
		case "==":
			if !v.Equals(cv) {
				return false
			}
		case ">=":
			if v.LT(cv) {
				return false
			}
		case "<=":
			if v.GT(cv) {
				return false
			}
		case ">":
			if !v.GT(cv) {
				return false
			}
		case "<":
			if !v.LT(cv) {
				return false
			}
		default:
			// unknown op -> fail
			return false
		}
	}
	return true
}

// config path helpers
func UserConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, DEFAULT_CONFIG_DIR)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", DEFAULT_CONFIG_DIR)
}

// DefaultConfig returns defaults.
func DefaultConfig() *config.Config {
	return &config.Config{
		RootURL: "",
		Arch:    "",
		OhosSdk: "",
		Channel: "stable",
	}
}

// DefaultArch returns combined OS-arch string used by server (like linux-x86_64).
func DefaultArch() string {
	goarch := runtime.GOARCH
	cfg, err := LoadConfig(DefaultConfigPath())
	if err != nil || cfg.Arch == "" {
		// WARN
		// fallback to os-arch
		return goarch
	}
	return cfg.Arch
}

func DefaultConfigPath() string {
	return filepath.Join(UserConfigDir(), "config.json")
}

func LoadConfig(path string) (*config.Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c config.Config
	if err := jsonUnmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func LoadLocalSdkInfo(ohosSdkPath string) (*meta.OhosSdkInfo, error) {
	packInfoPath := filepath.Join(ohosSdkPath, "toolchains", "oh-uni-package.json")
	if !IsFileExists(packInfoPath) {
		return nil, fmt.Errorf("invalid OHOS sdk directory tree: '%s' not found", packInfoPath)
	}
	data, err := os.ReadFile(packInfoPath)
	if err != nil {
		return nil, fmt.Errorf("OHOS sdk info read failed: %v", err)
	}

	// 解析JSON到结构体
	var config meta.OhosSdkInfo
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse OHOS sdk info failed: %v", err)
	}
	return &config, nil
}

func SaveConfig(path string, c *config.Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := jsonMarshalIndent(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// small json helpers to avoid import cycles
func jsonUnmarshal(b []byte, v interface{}) error {
	return json.Unmarshal(b, v)
}
func jsonMarshalIndent(v interface{}) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

// fetch URL bytes
func FetchURL(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func DownloadToFile(client *http.Client, url, dest string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func VerifyFileSHA256(path, expected string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	return strings.EqualFold(sum, expected), nil
}

// ExtractTarGz extracts tar.gz into destDir.
func ExtractTarGz(archive, destDir string) error {
	// Generate path for temporary .tar.gz file
	dir := filepath.Dir(archive)
	baseName := filepath.Base(archive)
	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)
	newTarGzName := nameWithoutExt + ".tar.gz"
	newTarGzPath := filepath.Join(dir, newTarGzName)

	// Copy original archive to temporary .tar.gz file using the extracted function
	if err := copyFile(archive, newTarGzPath); err != nil {
		return fmt.Errorf("failed to prepare .tar.gz file: %w", err)
	}

	// Clean up temporary file after extraction (success or failure)
	defer func() {
		if err := os.Remove(newTarGzPath); err != nil {
			fmt.Printf("warning: could not remove temporary file %s: %v", newTarGzPath, err)
		}
	}()

	// Extract the .tar.gz file
	if err := archiver.Unarchive(newTarGzPath, destDir); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	return nil
}

// list files (with dir) in a directory
func ListDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list directory failed: %w", err)
	}

	var filePaths []string
	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())
		filePaths = append(filePaths, fullPath)
	}

	return filePaths, nil
}

// copy all the contents (including links) in `srcDir` to `dstDir` (overwrite)
// e.g., {a/1.txt,a/b/c/2.txt} -> CopyDirContents(a, d) -> {d/1.txt,d/b/c/2.txt}

func CopyDirContents(srcDir, dstDir string) error {

	// Resolve absolute paths to detect overlaps
	absSrc, err := filepath.Abs(srcDir)
	if err != nil {
		return fmt.Errorf("failed to resolve source path: %w", err)
	}
	absDst, err := filepath.Abs(dstDir)
	if err != nil {
		return fmt.Errorf("failed to resolve destination path: %w", err)
	}

	// Prevent copying to itself or subdirectory
	if absSrc == absDst || strings.HasPrefix(absDst, absSrc+string(filepath.Separator)) {
		return fmt.Errorf("destination cannot be same as or inside source")
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("failed to read source directory: %w", err)
	}

	// Create destination directory if it doesn't exist
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	for _, entry := range entries {
		srcPath := filepath.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())

		if entry.IsDir() {
			// Recursively copy subdirectories
			if err := CopyDirContents(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			// Copy file
			if err := copyFile(srcPath, dstPath); err != nil {
				return fmt.Errorf("failed to copy file %s: %w", srcPath, err)
			}
		}
	}

	return nil
}
