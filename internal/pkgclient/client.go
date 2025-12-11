package pkgclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
		return errors.New("repo URL not configured (use --help for more info)")
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

	_, err := os.Stat(pkgPath)
	shouldDownload := os.IsNotExist(err)
	if !shouldDownload {
		// check checksum of downloaded packages
		ok, err := common.VerifyFileSHA256(pkgPath, choice.SHA256)
		if err != nil {
			return "", "", err
		}
		if !ok {
			// download to refresh
			fmt.Printf("the checksum of package '%s' in cache missmatch: download it\n", choice.Name)
			rmErr := os.Remove(pkgPath)
			if rmErr != nil {
				fmt.Printf("WARN: failed to remove outdated package '%s': %v\n", pkgPath, rmErr)
			}
			shouldDownload = true
		}
	}

	if shouldDownload {
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

// extract components (`common.GetInstallComponents()`) to `prefix`
//
// @return (extraction temp dir, error)
func (c *Client) extract(pkgPath, pkgName, pkgVersion, prefix string) (string, error) {
	// extract to prefix/<name>-<version>.tmp
	tmpDir := filepath.Join(prefix, fmt.Sprintf(".%s-%s.tmp", pkgName, pkgVersion))

	if err := os.MkdirAll(prefix, 0o755); err != nil {
		return tmpDir, err
	}
	// cleanup any previous tmp
	_ = os.RemoveAll(tmpDir)
	if err := common.ExtractTarGz(pkgPath, tmpDir); err != nil {
		return tmpDir, err
	}
	// copy components
	for _, component := range common.GetInstallComponents() {
		srcDir := filepath.Join(tmpDir, component)
		dstDir := filepath.Join(prefix, component)
		if !common.IsDirExists(srcDir) {
			if !common.IsOptionalInstallComponent(component) {
				fmt.Printf(" - WARN: package '%s' doesn't have component '%s'\n", pkgName, component)
			}
			continue
		}
		fmt.Printf(" - copying %s -> %s\n", srcDir, dstDir)
		if err := common.CopyDirContents(srcDir, dstDir); err != nil {
			return tmpDir, fmt.Errorf("failed to extract component '%s': %v", component, err)
		}
	}
	return tmpDir, nil
}

// @param[in] prefix only valid when toSdk == false
//
// @return (finalDir, error)
//
// @note prefix must be an absolute path
func (c *Client) install(pkgNameOrLocalFileList []string, prefix string, noConfirm bool) error {

	var localSdkInfo *meta.OhosSdkInfo
	var loadSdkErr error
	if localSdkInfo, loadSdkErr = common.LoadLocalSdkInfo(c.Config.OhosSdk); loadSdkErr != nil {
		return loadSdkErr
	}

	if c.Config.RootURL == "" {
		return fmt.Errorf("repo URL not configured (use --help for more info)")
	}

	if len(pkgNameOrLocalFileList) == 0 {
		return fmt.Errorf("empty install list")
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
	// assert lastArch != ""
	fmt.Printf("Resolving dependencies...\n")
	chosen, err := c.ResolveDependencies(pkgs, lastArch)
	if err != nil {
		return err
	}

	// ask for confirmation
	if !noConfirm {
		fmt.Printf("We are going to install (%s, API %s): \n", lastArch, localSdkInfo.ApiVersion)
		for name, e := range chosen {
			fmt.Printf(" - %s (%s)\n", name, e.Version)
		}
		fmt.Printf("--------------------------\n")
		fmt.Printf("Install Prefix: %s\n", prefix)
		fmt.Printf("--------------------------\n")
		ok, confirmErr := common.ConfirmAction(
			"Installation is irrevisible. Make sure to check your prefix before you proceed. (Y/[n]) ")
		if confirmErr != nil {
			return confirmErr
		}
		if !ok {
			fmt.Printf("Installation abort.\n")
			return nil
		}
	}

	// // open DB once
	// db, err := OpenDB(c.DBPath)
	// if err != nil {
	// 	return err
	// }
	// defer db.Close()

	for name, entry := range chosen {
		fmt.Printf("Preparing %s %s\n", name, entry.Version)

		// // check installed
		// installed, err := db.GetInstalled(name, prefix)
		// if err != nil {
		// 	return err
		// }
		// if installed != nil && installed.Version == entry.Version {
		// 	fmt.Printf(" - %s already installed at same version %s, skipping\n", name, entry.Version)
		// 	continue
		// }
		// if installed != nil && installed.Version != entry.Version {
		// 	// uninstall previous
		// 	if err := c.uninstallDB(db, name, prefix); err != nil {
		// 		return err
		// 	}
		// 	fmt.Printf(" - removed previous version %s\n", installed.Version)
		// }

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
		tmpDir, exErr := c.extract(curPkgPath, name, curPkgVer, prefix)
		if exErr != nil {
			return exErr
		}

		// patch libraries for development
		archDepRelPath, archErr := common.GetOhosArchDepLibDirRelPath(entry.Arch)
		if archErr != nil {
			return archErr
		}
		dstArchLibDir := filepath.Join(prefix, archDepRelPath)
		fmt.Printf("Patching libraries of package '%s'\n", name)
		c.patchLibFilesForCurrentInstallation(dstArchLibDir, prefix)
		// patch shared files like xorg libraries
		shareDir := filepath.Join(prefix, common.GetOhosSharedDirRelPath())
		if common.IsDirExists(shareDir) {
			// try to patch
			c.patchLibFilesForCurrentInstallation(shareDir, prefix)
		}
		// patch arch-dependent libs under arch-independent dir
		irregular, readErr := common.IsArchDepLibInArchIndepDir(prefix)
		if readErr != nil {
			return readErr
		}
		if irregular {
			fmt.Println(
				"WARN: current libraries install architecture-dependent library under architecture-independent directory, " +
					"and it may break your SDK env if you use different architectures. Take care of it")
			dstArchIndepLibDir := filepath.Join(prefix, common.GetOhosArchIndepLibDirRelPath())
			c.patchLibFilesForCurrentInstallation(dstArchIndepLibDir, prefix)
		}

		// executing script attachments
		if common.IsDirExists(tmpDir) {
			postInstScriptPath, found := common.GetPostInstScriptPath(tmpDir)
			if found {
				// execute it with install prefix
				fmt.Printf("Executing post-installation script...\n")
				outStr, exeErr := common.ExecuteShell(postInstScriptPath, prefix)
				if exeErr != nil {
					return exeErr
				}
				fmt.Println("##################################")
				if strings.TrimSpace(outStr) == "" {
					fmt.Print("(empty output)")
				} else {
					fmt.Print(outStr)
				}
				fmt.Println("\n##################################")
			}
			// clean up temporary directory
			fmt.Println("Cleaning temporary files...")
			// ignore errors
			os.RemoveAll(tmpDir)
		}

		// // record in DB
		// if err := db.InsertInstalled(name, curPkgVer, entry.Arch, prefix, finalDir); err != nil {
		// 	return err
		// }

		fmt.Printf("Installed %s %s -> %s\n\n", name, curPkgVer, prefix)
	}

	fmt.Printf("\nFinish installation: %d packages installed\n\n", len(chosen))

	return nil
}

// for normal installation: use tgtLibdir == installLibdir
func (c *Client) patchLibFilesForCurrentInstallation(libdir, installPrefix string) error {
	return c.PatchLibFiles(libdir, libdir, installPrefix)
}

// PatchLibFiles patches .la and .pc files in libdir similarly to the shell snippet.
// libdir must be an absolute or relative path.
// tgtLibdir is the directory that libraries actually in.
// installLibdir & installPrefix is the libdir/prefix in the configuration files.
// Returns an error if one or more file operations fail.
// NOTE: libdir and installPrefix must be absolute paths
func (c *Client) PatchLibFiles(tgtLibdir, installLibdir, installPrefix string) error {
	if !common.IsDirExists(tgtLibdir) {
		fmt.Printf(" - WARN: specific directory '%s' not exists while patching libraries. Skipped\n", tgtLibdir)
		return nil
	}

	var errors []string

	// 1) patch *.la files: replace libdir='.*' -> libdir='$installLibdir'
	laPattern := filepath.Join(tgtLibdir, "*.la")
	laFiles, err := filepath.Glob(laPattern)
	if err != nil {
		return fmt.Errorf("glob %q: %w", laPattern, err)
	}

	reLibdirLa := regexp.MustCompile(`libdir='.*'`)
	for _, la := range laFiles {
		info, statErr := os.Stat(la)
		if statErr != nil {
			// If file disappeared between glob and stat, skip
			errors = append(errors, fmt.Sprintf("stat %s: %v", la, statErr))
			continue
		}
		if !info.Mode().IsRegular() {
			fmt.Printf(" - skip irregular file '%s'", info.Name())
			continue
		}

		fmt.Printf(" - patching library archive file generated by libtool: %s\n", la)

		content, readErr := os.ReadFile(la)
		if readErr != nil {
			errors = append(errors, fmt.Sprintf("read %s: %v", la, readErr))
			continue
		}

		libDirInLa := fmt.Sprintf("libdir='%s'", installLibdir)
		mod := reLibdirLa.ReplaceAll(content, []byte(libDirInLa))

		// Only write if changed
		if string(mod) != string(content) {
			if writeErr := os.WriteFile(la, mod, info.Mode().Perm()); writeErr != nil {
				errors = append(errors, fmt.Sprintf("write %s: %v", la, writeErr))
				continue
			}
		}
	}

	// 2) patch pkgconfig/*.pc files:
	pcPattern := filepath.Join(tgtLibdir, "pkgconfig", "*.pc")
	pcFiles, err := filepath.Glob(pcPattern)
	if err != nil {
		return fmt.Errorf("glob %q: %w", pcPattern, err)
	}

	// use multiline regex anchors to replace full lines:
	rePrefix := regexp.MustCompile(`(?m)^prefix=.*`)
	reLibdirPc := regexp.MustCompile(`(?m)^libdir=.*`)
	reIncludedir := regexp.MustCompile(`(?m)^(includedir=).*(/include.*)$`)
	for _, pc := range pcFiles {
		info, statErr := os.Stat(pc)
		if statErr != nil {
			errors = append(errors, fmt.Sprintf("stat %s: %v", pc, statErr))
			continue
		}
		if !info.Mode().IsRegular() {
			fmt.Printf(" - skip irregular file '%s'", info.Name())
			continue
		}

		fmt.Printf(" - patching pkg-config file generated by Makefile: %s\n", pc)

		contentBytes, readErr := os.ReadFile(pc)
		if readErr != nil {
			errors = append(errors, fmt.Sprintf("read %s: %v", pc, readErr))
			continue
		}
		content := string(contentBytes)

		content = rePrefix.ReplaceAllString(content, "prefix="+installPrefix)
		// replace libdir line with the installLibdir value
		content = reLibdirPc.ReplaceAllString(content, "libdir="+installLibdir)
		content = reIncludedir.ReplaceAllString(content, "${1}"+installPrefix+"${2}")

		// Only write if changed
		if content != string(contentBytes) {
			if writeErr := os.WriteFile(pc, []byte(content), info.Mode().Perm()); writeErr != nil {
				errors = append(errors, fmt.Sprintf("write %s: %v", pc, writeErr))
				continue
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("some operations failed while patching libraries:\n%s", strings.Join(errors, "\n"))
	}
	return nil
}

// ResolveDependencies takes initial requested package names (each string may be a simple name)
// and returns a map[name]IndexEntry of chosen versions to install (values order not guaranteed).
// It uses index.json and package manifests for transitive deps.
func (c *Client) ResolveDependencies(requested []string, arch string) (map[string]meta.IndexEntry, error) {
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
		if arch != e.Arch {
			continue
		}
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

	// initial requested: they may be plain names/empty
	for _, r := range requested {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		depName, depConstraints, depErr := common.ParseDep(r)
		if depErr != nil {
			return nil, fmt.Errorf("error while resolving dependencies for '%s': %+v", r, depErr)
		}
		oldConstraints, hasConstraints := constraints[depName]
		if hasConstraints {
			constraints[depName] = append(oldConstraints, depConstraints)
		} else {
			// first time check for depName: add to queue
			constraints[depName] = []common.Constraint{depConstraints}
			queue = append(queue, depName)
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
		curConstraints := constraints[name]
		var chosenEntry *meta.IndexEntry
		for _, e := range candList {
			if common.SatisfiesConstraints(e.Version, curConstraints) && e.OhosApi == sdkInfo.ApiVersion {
				tmp := e
				chosenEntry = &tmp
				break
			}
		}
		if chosenEntry == nil {
			// no candidate found
			return nil, fmt.Errorf("no version of %s satisfies constraints %+v and OHOS API %s",
				name, curConstraints, sdkInfo.ApiVersion)
		}

		// select it
		chosen[name] = *chosenEntry

		// get its declared depends
		curDeps := chosenEntry.Depends
		// iterate declared dependencies and merge constraints
		for _, dep := range curDeps {
			depName, depC, parseErr := common.ParseDep(dep)
			if parseErr != nil {
				return nil, fmt.Errorf("error while resolving dependencies for '%s': %+v", dep, parseErr)
			}
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
func (c *Client) InstallToSdk(pkgNameOrLocalFileList []string, noConfirm bool) error {
	if c.Config.OhosSdk == "" {
		return errors.New("OHOS SDK path not configured (use --help for more info)")
	}
	prefix := filepath.Join(c.Config.OhosSdk, "native", "sysroot", "usr")
	if !common.IsDirExists(prefix) {
		return fmt.Errorf("invalid OHOS sdk directory tree: directory '%s' not exists", prefix)
	}
	return c.install(pkgNameOrLocalFileList, prefix, noConfirm)
}

// Install downloads and installs the named package into prefix.
// @note prefix must be an absolute path
func (c *Client) Install(pkgNameOrLocalFileList []string, prefix string, noConfirm bool) error {

	return c.install(pkgNameOrLocalFileList, prefix, noConfirm)
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
