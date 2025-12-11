package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/pkg/meta"
	"github.com/blang/semver/v4"
	"github.com/spf13/cobra"
)

func main() {
	var payloadDir, outDir, arch, ohosAPI, name, version string
	var rawDepends, depends []string
	var noArchLibIsolation bool

	root := &cobra.Command{
		Use:   "oh-pkgtool",
		Short: "Create a package (.pkg) and manifest from a payload directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if payloadDir == "" || name == "" || version == "" || arch == "" || ohosAPI == "" {
				return fmt.Errorf("payloadDir, name, version, arch and OHOS API are required")
			}
			if outDir == "" {
				outDir = "."
			}
			for _, rawDep := range rawDepends {
				for _, part := range strings.Split(rawDep, ",") {
					dep := strings.TrimSpace(part)
					if dep != "" {
						depends = append(depends, dep)
					}
				}
			}

			return buildPackage(payloadDir, outDir, name, version, arch, ohosAPI, depends, !noArchLibIsolation)
		},
	}

	root.Flags().StringVarP(&payloadDir, "payload", "i", "", "package payload directory (to be packaged)")
	root.Flags().StringVarP(&outDir, "out", "o", ".", "output directory for .pkg and manifest")
	root.Flags().StringVarP(&arch, "arch", "a", "", "target arch (e.g. amd64,arm,risv64) (required)")
	root.Flags().StringVar(&ohosAPI, "api", "", "target OpenHarmony SDK API (e.g. 12,14,15) (required)")
	root.Flags().StringVarP(&name, "name", "n", "", "package name (required)")
	root.Flags().StringVarP(&version, "version", "v", "", "package version (required)")
	root.Flags().StringArrayVar(&rawDepends, "depends", nil, "dependency (can be repeated). Examples: \"libz>=1.2.11\", \"openssl\", \"libfoo==1.0.0\"")
	root.Flags().BoolVar(&noArchLibIsolation, "no-archlib-isolation", false, "use architecture-dependent library isolation at packaging time (default FALSE)")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func buildPackage(payloadDir, outDir, name, version, arch, ohosAPI string, deps []string, archLibIsolation bool) error {
	if _, err := os.Stat(payloadDir); err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// validate package name
	if strings.ContainsAny(name, common.GetInvalidPkgNameCharsInStr()) {
		return fmt.Errorf("invalid token set '%s' in package name '%s'", common.GetInvalidPkgNameCharsInStr(), name)
	}
	// validate version
	if _, err := semver.ParseTolerant(version); err != nil {
		return fmt.Errorf("invalid version '%s'", version)
	}
	// validate arch
	var archErr error
	arch, archErr = common.MapArchStr(arch)
	if archErr != nil {
		return archErr
	}
	// validate API
	if _, err := strconv.Atoi(ohosAPI); err != nil {
		return fmt.Errorf("invalid OHOS API version: '%s'", ohosAPI)
	}
	// validate deps
	for _, dep := range deps {
		if _, _, err := common.ParseDep(dep); err != nil {
			return err
		}
	}

	pkgName := common.GenPkgFileName(name, version, arch, ohosAPI)
	manifestName := common.GenPkgManifestName(name, version, arch, ohosAPI)
	pkgPath := filepath.Join(outDir, pkgName)
	manifestPath := filepath.Join(outDir, manifestName)

	// validate payloadDir
	if err := checkPayloadDirTree(payloadDir, arch, archLibIsolation); err != nil {
		return err
	}

	// check script attachment
	_, found := common.GetPostInstScriptPath(payloadDir)
	if found {
		fmt.Println("NOTE: post-installation script detected")
	}

	// create tar.gz without libexec
	if err := common.TarGzDir(payloadDir, pkgPath, []string{}, common.GetInstallExcluded()); err != nil {
		return err
	}
	sum, err := common.ComputeSHA256(pkgPath)
	if err != nil {
		return err
	}
	sz, err := os.Stat(pkgPath)
	if err != nil {
		return err
	}

	m := &meta.Manifest{
		Name:    name,
		Version: version,
		Arch:    arch,
		OhosApi: ohosAPI,
		Format:  1,
		Size:    sz.Size(),
		SHA256:  sum,
		Depends: deps,
	}
	if err := common.WriteManifest(manifestPath, m); err != nil {
		return err
	}
	fmt.Printf("Wrote %s and %s\n", pkgPath, manifestPath)
	return nil
}

func checkPayloadDirTree(payloadDir, arch string, archLibIsolation bool) error {

	archIndepLibDir := filepath.Join(payloadDir, "lib")
	archDepLibDirRelPath, archErr := common.GetOhosArchDepLibDirRelPath(arch)
	archDepLibDir := filepath.Join(payloadDir, archDepLibDirRelPath)
	if archErr != nil {
		return archErr
	}
	// archHeaderDir := filepath.Join(payloadDir, "include")
	// archShareDir := filepath.Join(payloadDir, "share")
	// archBinDir := filepath.Join(payloadDir, "bin")
	// archSbinDir := filepath.Join(payloadDir, "sbin")
	archLibexecDir := filepath.Join(payloadDir, "libexec")

	if common.IsDirExists(archIndepLibDir) {
		// check architecture indenpendent library directory
		entries, err := os.ReadDir(archIndepLibDir)
		if err != nil {
			return fmt.Errorf("failed to read dir '%s' while checking payload dir tree", archIndepLibDir)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			filename := entry.Name()
			if common.IsArchDependentLib(filepath.Join(archIndepLibDir, filename)) {
				msg := "architecture-specific library files were compiled and installed in an architecture-independent directory," +
					" which will cause architecture conflicts when users install the library to OHOS SDK (especially for cmake project). "
				if archLibIsolation {
					return fmt.Errorf(
						"%s This behavior is disabled by default. Please check the --libdir parameter used during compilation if this is not what you want. "+
							"Otherwise, rerun with --no-archlib-isolation", msg)
				} else {
					fmt.Println("WARN: " + msg)
				}
				break
			}
		}
		// not recursive exception: arch-dependent libraries with its own directory (in arch-independent lib dir) is fine. Like python

		if !common.IsDirExists(archDepLibDir) {
			fmt.Println("WARN: architecture-dependent libraries not found for this package")
		}
	} else {
		fmt.Println("WARN: libraries not found for this package")
	}

	// TODO: not support libexec for now
	if common.IsDirExists(archLibexecDir) {
		fmt.Println("WARN: executable libraries will be ignored in this package")
	}

	return nil
}
