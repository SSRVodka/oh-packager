package pkgclient

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/pkg/meta"
)

// XCompile builds packages from source in topological order
func (c *Client) XCompile(packageNames []string, arch string) error {
	if c.Config.PkgSrcRepo == "" {
		return fmt.Errorf("package source repository for cross compile not configured")
	}

	fmt.Printf("Cross-compiling for architecture: %s\n", arch)
	fmt.Printf("Requested packages: %s\n\n", strings.Join(packageNames, ", "))

	repo := c.Config.PkgSrcRepo

	// generate VERSION file
	genSh := filepath.Join(repo, "gen-versions.sh")
	out, genErr := common.ExecuteShell(genSh)
	if genErr != nil {
		return fmt.Errorf("failed to generate VERSION metadata: %v; Output: %s", genErr, out)
	}

	// Parse VERSION file from package source repository
	versionFilePath := filepath.Join(repo, "VERSION")

	// Check if VERSION file exists
	if !common.IsFileExists(versionFilePath) {
		return fmt.Errorf("VERSION file not found at %s. Please ensure package source repo is available", versionFilePath)
	}

	fmt.Println("Parsing VERSION file...")
	allPackages, err := common.ParseVersionFile(versionFilePath)
	if err != nil {
		return fmt.Errorf("failed to parse VERSION file: %w", err)
	}

	fmt.Printf("Found %d packages in VERSION file\n", len(allPackages))

	// Filter to requested packages and their dependencies
	selectedPackages, err := c.selectPackagesWithDeps(allPackages, packageNames)
	if err != nil {
		return err
	}

	fmt.Printf("Selected %d packages (including dependencies)\n\n", len(selectedPackages))

	// Perform topological sort
	fmt.Println("Computing build order...")
	buildOrder, err := TopologicalSort(selectedPackages)
	if err != nil {
		return fmt.Errorf("failed to compute build order: %w", err)
	}

	// Print the dependency graph
	PrintDependencyGraph(selectedPackages, buildOrder)

	// Construct parameters for builder shell
	builderParams := []string{fmt.Sprintf("--cpu=%s", arch)}
	for _, name := range buildOrder {
		builderParams = append(builderParams, filepath.Join(repo, name, "BUILD"))
	}

	// change working directory
	chdirErr := os.Chdir(repo)
	if chdirErr != nil {
		return chdirErr
	}
	shErr := common.ExecuteShellRealTime(filepath.Join(repo, "builder.sh"), builderParams...)

	if shErr != nil {
		return shErr
	}

	fmt.Printf("Package(s) Build Success. Output Dir: '%s/dist.%s.*'\n", repo, arch)

	return nil
}

// selectPackagesWithDeps recursively collects packages and their dependencies
func (c *Client) selectPackagesWithDeps(allPackages []*meta.PackageInfo, requestedNames []string) ([]*meta.PackageInfo, error) {
	pkgMap := make(map[string]*meta.PackageInfo)
	for _, pkg := range allPackages {
		pkgMap[pkg.Name] = pkg
	}

	selected := make(map[string]*meta.PackageInfo)
	var visit func(name string) error

	visit = func(name string) error {
		if _, visited := selected[name]; visited {
			return nil
		}

		pkg, exists := pkgMap[name]
		if !exists {
			return fmt.Errorf("package not found in VERSION file: %s", name)
		}

		selected[name] = pkg

		// Visit runtime dependencies
		for _, dep := range pkg.Depends {
			depName := common.NormalizeDependency(dep)
			if err := visit(depName); err != nil {
				return err
			}
		}

		// Visit build-time dependencies
		for _, dep := range pkg.BuildDepends {
			depName := common.NormalizeDependency(dep)
			if err := visit(depName); err != nil {
				return err
			}
		}

		return nil
	}

	// Visit all requested packages
	for _, name := range requestedNames {
		if err := visit(name); err != nil {
			return nil, err
		}
	}

	// Convert map to slice
	var result []*meta.PackageInfo
	for _, pkg := range selected {
		result = append(result, pkg)
	}

	return result, nil
}
