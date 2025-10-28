package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/pkg/meta"
	"github.com/spf13/cobra"
)

func main() {
	var payloadDir, outDir, arch, name, version string

	root := &cobra.Command{
		Use:   "oh-pkgtool",
		Short: "Create a package (.pkg) and manifest from a payload directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if payloadDir == "" || name == "" || version == "" || arch == "" {
				return fmt.Errorf("payloadDir, name, version and arch are required")
			}
			if outDir == "" {
				outDir = "."
			}
			return buildPackage(payloadDir, outDir, name, version, arch)
		},
	}

	root.Flags().StringVarP(&payloadDir, "payload", "i", "", "package payload directory (to be packaged)")
	root.Flags().StringVarP(&outDir, "out", "o", ".", "output directory for .pkg and manifest")
	root.Flags().StringVarP(&arch, "arch", "a", "", "target arch (e.g. amd64,arm,risv64) (required)")
	root.Flags().StringVarP(&name, "name", "n", "", "package name (required)")
	root.Flags().StringVarP(&version, "version", "v", "", "package version (required)")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func buildPackage(payloadDir, outDir, name, version, arch string) error {
	if _, err := os.Stat(payloadDir); err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	var archErr error
	arch, archErr = common.MapArchStr(arch)
	if archErr != nil {
		return archErr
	}
	pkgName := common.GenPkgFileName(name, version, arch)
	manifestName := common.GenPkgManifestName(name, version, arch)
	pkgPath := filepath.Join(outDir, pkgName)
	manifestPath := filepath.Join(outDir, manifestName)

	// create tar.gz
	if err := common.TarGzDir(payloadDir, pkgPath); err != nil {
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
		Format:  1,
		Size:    sz.Size(),
		SHA256:  sum,
	}
	if err := common.WriteManifest(manifestPath, m); err != nil {
		return err
	}
	fmt.Printf("Wrote %s and %s\n", pkgPath, manifestPath)
	return nil
}
