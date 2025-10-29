package pkgclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/pkg/config"
	"github.com/SSRVodka/oh-packager/pkg/meta"
	"github.com/blang/semver/v4"
)

// Client holds runtime info.
type Client struct {
	Config *config.Config
	Cache  string
	DBPath string
	HTTP   *http.Client
}

// NewClient constructs client with default cache/db paths under config dir.
func NewClient(cfg *config.Config) *Client {
	cfgDir := common.UserConfigDir()
	cache := filepath.Join(cfgDir, "cache")
	db := filepath.Join(cfgDir, "installed.db")
	_ = os.MkdirAll(cache, 0o755)
	return &Client{
		Config: cfg,
		Cache:  cache,
		DBPath: db,
		HTTP:   &http.Client{},
	}
}

// ListPackages fetches index.json and prints packages for arch.
func (c *Client) ListPackages(arch string) error {
	if c.Config.RootURL == "" {
		return errors.New("root URL not configured (use --help for more info)")
	}
	// Some deployments put channels directly under root; try both patterns.
	// Try root/channels/<channel>/index.json
	tryURLs := []string{
		fmt.Sprintf("%s/channels/%s/index.json", strings.TrimRight(c.Config.RootURL, "/"), c.Config.Channel),
		fmt.Sprintf("%s/%s/channels/%s/index.json", strings.TrimRight(c.Config.RootURL, "/"), "repo", c.Config.Channel),
	}
	var idxBytes []byte
	var err error
	for _, u := range tryURLs {
		idxBytes, err = common.FetchURL(c.HTTP, u)
		if err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("failed fetching index.json: %w", err)
	}

	var idx meta.Index
	if err := json.Unmarshal(idxBytes, &idx); err != nil {
		return err
	}
	entries := []meta.IndexEntry{}
	for _, e := range idx.Packages {
		if e.Arch == arch {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		fmt.Println("no packages for", arch)
		return nil
	}
	// Group by name and list latest version first
	byName := map[string][]meta.IndexEntry{}
	for _, e := range entries {
		byName[e.Name] = append(byName[e.Name], e)
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		list := byName[n]
		// sort by semver desc
		sort.Slice(list, func(i, j int) bool {
			vi, _ := semver.ParseTolerant(list[i].Version)
			vj, _ := semver.ParseTolerant(list[j].Version)
			return vi.GT(vj)
		})
		latest := list[0]
		fmt.Printf("%s\t%s\tAPI: %s\t%s\n", latest.Name, latest.Version, latest.OhosApi, latest.URL)
	}
	return nil
}

/** @return (pkgFilePath, pkgVersion, error) */
func (c *Client) download(choice meta.IndexEntry) (string, string, error) {
	// download package
	pkgURL := common.JoinURL(c.Config.RootURL, choice.URL)
	pkgPath := filepath.Join(c.Cache, filepath.Base(choice.URL))
	if _, err := os.Stat(pkgPath); os.IsNotExist(err) {
		fmt.Println(" - downloading", pkgURL)
		if err := common.DownloadToFile(c.HTTP, pkgURL, pkgPath); err != nil {
			return "", "", err
		}
	}
	// verify checksum
	ok, err := common.VerifyFileSHA256(pkgPath, choice.SHA256)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("checksum mismatch for %s", pkgPath)
	}
	return pkgPath, choice.Version, nil
}

/** @return (finalDir, error) */
func (c *Client) extract(pkgPath, pkgName, pkgVersion, prefix string) (string, error) {
	// extract to prefix/<name>-<version>.tmp -> then rename to <name>-<version>
	tmpDir := filepath.Join(prefix, fmt.Sprintf(".%s-%s.tmp", pkgName, pkgVersion))
	finalDir := filepath.Join(prefix, fmt.Sprintf("%s-%s", pkgName, pkgVersion))
	link := filepath.Join(prefix, pkgName)

	if err := os.MkdirAll(prefix, 0o755); err != nil {
		return "", err
	}
	// cleanup any previous tmp
	_ = os.RemoveAll(tmpDir)
	if err := common.ExtractTarGz(pkgPath, tmpDir); err != nil {
		return "", err
	}
	// rename tmp to final (atomic on same fs)
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return "", err
	}
	// update symlink atomically
	_ = os.Remove(link)
	if err := os.Symlink(filepath.Base(finalDir), link); err != nil {
		return "", err
	}
	return finalDir, nil
}

/**
 * @param[in] prefix only valid when toSdk == false
 * @return (finalDir, error)
 */
func (c *Client) install(pkgNameOrLocalFileList []string, toSdk bool, prefix string) error {

	var localSdkInfo *meta.OhosSdkInfo
	var loadSdkErr error
	if localSdkInfo, loadSdkErr = common.LoadLocalSdkInfo(c.Config.OhosSdk); loadSdkErr != nil {
		return loadSdkErr
	}

	if toSdk {
		prefix = filepath.Join(c.Config.OhosSdk, "native", "sysroot", "usr")
		if !common.IsDirExists(prefix) {
			return fmt.Errorf("invalid OHOS sdk directory tree: directory '%s' not exists", prefix)
		}
	}

	lastArch := ""
	name2pkgPath := map[string]string{}

	// name/constraint list
	pkgs := []string{}

	for _, pkgNameOrLocalFile := range pkgNameOrLocalFileList {
		var pkgName, pkgPath, ver, arch string
		var api = localSdkInfo.ApiVersion
		if common.IsPkgPath(pkgNameOrLocalFile) {
			// install from local file: lock constraint in filename
			var parseErr error
			pkgName, ver, arch, api, parseErr = common.ParsePkgNameFromPath(pkgNameOrLocalFile)
			if parseErr != nil {
				return parseErr
			}
			pkgPath = pkgNameOrLocalFile
			// add pkgPath into result
			name2pkgPath[pkgName] = pkgPath
			// build constraint string
			pkgName = pkgName + " == " + ver
			// check SDK API
			if api != localSdkInfo.ApiVersion {
				return fmt.Errorf("API version mismatch with your local configured SDK: '%s' vs '%s'",
					api, localSdkInfo.ApiVersion)
			}
		} else {
			// install from server using pkgName
			pkgName = pkgNameOrLocalFile
			arch = common.DefaultArch()
		}

		// check arch consistency
		if lastArch == "" {
			lastArch = arch
		} else if arch != lastArch {
			return fmt.Errorf("different archs in one installation: '%s' vs '%s'", arch, lastArch)
		}

		pkgs = append(pkgs, pkgName)
	}

	// Resolve dependencies (returns chosen versions map)
	fmt.Printf("Resolving dependencies...\n")
	chosen, err := c.ResolveDependencies(pkgs)
	if err != nil {
		return err
	}

	// open DB once
	db, err := OpenDB(c.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	for name, entry := range chosen {
		fmt.Printf("Preparing %s %s\n", name, entry.Version)

		// check installed
		installed, err := db.GetInstalled(name, prefix)
		if err != nil {
			return err
		}
		if installed != nil && installed.Version == entry.Version {
			fmt.Printf(" - %s already installed at same version %s, skipping\n", name, entry.Version)
			continue
		}
		if installed != nil && installed.Version != entry.Version {
			// uninstall previous
			if err := c.uninstallDB(db, name, prefix); err != nil {
				return err
			}
			fmt.Printf(" - removed previous version %s\n", installed.Version)
		}

		var curPkgPath, curPkgVer string
		if f, ok := name2pkgPath[name]; !ok {
			var derr error
			curPkgPath, curPkgVer, derr = c.download(entry)
			if derr != nil {
				return derr
			}
			name2pkgPath[name] = curPkgPath
		} else {
			curPkgPath = f
			curPkgVer = entry.Version
			fmt.Printf(" - using local file: %s\n", curPkgPath)
		}

		fmt.Printf("Extracting %s %s\n", name, curPkgVer)
		finalDir, exErr := c.extract(curPkgPath, name, curPkgVer, prefix)
		if exErr != nil {
			return exErr
		}

		if toSdk {
			srcLibDir := filepath.Join(finalDir, "lib")
			dstLibDir := filepath.Join(prefix, "lib")
			archDepSrcLibDir := filepath.Join(srcLibDir, entry.Arch+"-linux-ohos")
			archDepDstLibDir := filepath.Join(dstLibDir, entry.Arch+"-linux-ohos")
			if !common.IsDirExists(archDepDstLibDir) {
				return fmt.Errorf("invalid OHOS sdk directory tree: lib dir '%s' not exists", dstLibDir)
			}
			srcHeaderDir := filepath.Join(finalDir, "include")
			dstHeaderDir := filepath.Join(prefix, "include")
			if !common.IsDirExists(dstHeaderDir) {
				return fmt.Errorf("invalid OHOS sdk directory tree: header dir '%s' not exists", dstHeaderDir)
			}
			srcShareDir := filepath.Join(finalDir, "share")
			dstShareDir := filepath.Join(prefix, "share")
			if !common.IsDirExists(dstShareDir) {
				return fmt.Errorf("invalid OHOS sdk directory tree: share dir '%s' not exists", dstShareDir)
			}
			// check arch-dep libs in arch-indep directory
			var libFiles []string
			var err error
			// Check if there are architecture dependent libraries in dstLibDir
			if libFiles, err = common.ListDir(srcLibDir); err != nil {
				return err
			}
			for _, l := range libFiles {
				if common.IsArchDependentLib(l) {
					fmt.Printf(
						" - WARNING: architecture-dependent library '%s' in arch-independent directory; This library may not be configured correctly.\n"+
							"If this is not what you want, please clean build cache & make sure you've setup flags correctly like --libdir at compile time\n", l)
				}
			}

			if common.IsDirExists(archDepSrcLibDir) {
				fmt.Printf(" - Copying libraries (in '%s') to sdk...\n", name)
				if err := common.CopyDirContents(archDepSrcLibDir, archDepDstLibDir); err != nil {
					return err
				}
			} else {
				fmt.Printf(" - NOTE: package does NOT have any architecture-dependent libraries for OHOS\n")
			}
			if common.IsDirExists(srcHeaderDir) {
				fmt.Printf(" - Copying headers (in '%s') to sdk...\n", name)
				if err := common.CopyDirContents(srcHeaderDir, dstHeaderDir); err != nil {
					return err
				}
			} else {
				fmt.Printf(" - NOTE: package does NOT have any headers\n")
			}
			if common.IsDirExists(srcShareDir) {
				fmt.Printf(" - Copying shared resources (in '%s') to sdk\n", name)
				if err := common.CopyDirContents(srcShareDir, dstShareDir); err != nil {
					return err
				}
			} else {
				fmt.Printf(" - NOTE: package does NOT have any shared resources\n")
			}

			// remove the redundant dirs (ignore errors)
			os.RemoveAll(finalDir)
			os.RemoveAll(filepath.Join(prefix, name))
		} else {
			// ONLY record for non-sdk installation
			// record in DB
			if err := db.InsertInstalled(name, curPkgVer, entry.Arch, prefix, finalDir); err != nil {
				return err
			}
			fmt.Printf("Installed %s %s -> %s\n", name, curPkgVer, finalDir)
		}
	}

	return nil
}

// ResolveDependencies takes initial requested package names (each string may be a simple name)
// and returns a map[name]IndexEntry of chosen versions to install (values order not guaranteed).
// It uses index.json and package manifests for transitive deps.
func (c *Client) ResolveDependencies(requested []string) (map[string]meta.IndexEntry, error) {
	// load index
	idx, err := c.loadIndex()
	if err != nil {
		return nil, err
	}
	// load local sdk info
	sdkInfo, err := common.LoadLocalSdkInfo(c.Config.OhosSdk)
	if err != nil {
		return nil, err
	}

	// build entries-by-name map from index
	byName := map[string][]meta.IndexEntry{}
	for _, e := range idx.Packages {
		byName[e.Name] = append(byName[e.Name], e)
	}
	// sort each list by semver descending
	for _, list := range byName {
		sort.SliceStable(list, func(i, j int) bool {
			vi, _ := semver.ParseTolerant(list[i].Version)
			vj, _ := semver.ParseTolerant(list[j].Version)
			return vi.GT(vj)
		})
	}

	// constraints map: name -> []Constraint
	constraints := map[string][]common.Constraint{}
	queue := []string{}

	// initial requested: they may be plain names. For now we treat requested as name-only.
	for _, r := range requested {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if _, ok := constraints[r]; !ok {
			constraints[r] = []common.Constraint{}
			queue = append(queue, r)
		}
	}

	// result map chosen[name] = IndexEntry
	chosen := map[string]meta.IndexEntry{}

	// BFS-like process: while queue has names, attempt to pick a version satisfying constraints,
	// fetch its manifest, and enqueue its dependencies (merging constraints if present).
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]

		// if already chosen, continue
		if _, ok := chosen[name]; ok {
			continue
		}

		// find candidates for this name
		candList := byName[name]
		if len(candList) == 0 {
			return nil, fmt.Errorf("dependency %q not found in index", name)
		}
		// pick first (latest) candidate satisfying constraints[name]
		cands := constraints[name]
		var chosenEntry *meta.IndexEntry
		for _, e := range candList {
			if common.SatisfiesConstraints(e.Version, cands) && e.OhosApi == sdkInfo.ApiVersion {
				tmp := e
				chosenEntry = &tmp
				break
			}
		}
		if chosenEntry == nil {
			// no candidate found
			return nil, fmt.Errorf("no version of %s satisfies constraints %+v and OHOS API %s",
				name, cands, sdkInfo.ApiVersion)
		}

		// select it
		chosen[name] = *chosenEntry

		// get its declared depends
		curDeps := chosenEntry.Depends
		// iterate declared dependencies and merge constraints
		for _, dep := range curDeps {
			depName, depC := common.ParseDep(dep)
			// append constraint
			cur := constraints[depName]
			// if depName not seen before, queue it
			if _, ok := constraints[depName]; !ok {
				queue = append(queue, depName)
			}
			constraints[depName] = append(cur, depC)
		}
	}

	return chosen, nil
}

// Install downloads and installs the named package into OHOS sdk
func (c *Client) InstallToSdk(pkgNameOrLocalFileList []string) error {

	return c.install(pkgNameOrLocalFileList, true, "")
}

// Install downloads and installs the named package into prefix.
func (c *Client) Install(pkgNameOrLocalFileList []string, prefix string) error {

	return c.install(pkgNameOrLocalFileList, false, prefix)
}

// Uninstall removes installed package from prefix.
func (c *Client) Uninstall(pkgName, prefix string) error {
	db, err := OpenDB(c.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	return c.uninstallDB(db, pkgName, prefix)
}

func (c *Client) uninstallDB(db *DB, pkgName, prefix string) error {
	inst, err := db.GetInstalled(pkgName, prefix)
	if err != nil {
		return err
	}
	if inst == nil {
		return fmt.Errorf("%s not installed in %s", pkgName, prefix)
	}
	link := filepath.Join(prefix, pkgName)
	// remove symlink if points to installed path
	if ltarget, err := os.Readlink(link); err == nil {
		if ltarget == inst.Path {
			_ = os.Remove(link)
		}
	}
	// remove installed dir
	if err := os.RemoveAll(inst.Path); err != nil {
		return err
	}
	if err := db.DeleteInstalled(pkgName, prefix); err != nil {
		return err
	}
	fmt.Printf("uninstalled %s from %s\n", pkgName, prefix)
	return nil
}

// Helpers

func (c *Client) loadIndex() (*meta.Index, error) {
	try := []string{
		fmt.Sprintf("%s/channels/%s/index.json", strings.TrimRight(c.Config.RootURL, "/"), c.Config.Channel),
		fmt.Sprintf("%s/%s/channels/%s/index.json", strings.TrimRight(c.Config.RootURL, "/"), "repo", c.Config.Channel),
	}
	var lastErr error
	for _, u := range try {
		b, err := common.FetchURL(c.HTTP, u)
		if err != nil {
			lastErr = err
			continue
		}
		var idx meta.Index
		if err := json.Unmarshal(b, &idx); err != nil {
			return nil, err
		}
		return &idx, nil
	}
	return nil, fmt.Errorf("failed to fetch index.json: %v", lastErr)
}
