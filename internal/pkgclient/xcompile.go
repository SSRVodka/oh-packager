package pkgclient

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/pkg/meta"
)

type buildTask struct {
	name     string
	depsFile string
	logPath  string
}

type buildResult struct {
	name       string
	artifactID string
	logPath    string
	cacheHit   bool
	err        error
}

// XCompile builds packages from source in topological order.
func (c *Client) XCompile(packageNames []string, arch string, jobs int) error {
	if c.Config.PkgSrcRepo == "" {
		return fmt.Errorf("package source repository for cross compile not configured")
	}
	if jobs < 1 {
		return fmt.Errorf("jobs must be >= 1")
	}

	fmt.Printf("Cross-compiling for architecture: %s\n", arch)
	fmt.Printf("Build jobs: %d\n", jobs)
	fmt.Printf("Requested packages: %s\n\n", strings.Join(packageNames, ", "))

	repo := c.Config.PkgSrcRepo

	// Generate PKG_INDEX.json from BUILD metadata.
	genSh := filepath.Join(repo, "gen-pkg-index.sh")
	out, genErr := common.ExecuteShell(genSh)
	if genErr != nil {
		return fmt.Errorf("failed to generate package index metadata: %v; Output: %s", genErr, out)
	}

	indexFilePath := filepath.Join(repo, "PKG_INDEX.json")
	if !common.IsFileExists(indexFilePath) {
		return fmt.Errorf("package index not found at %s. Please ensure package source repo is available", indexFilePath)
	}

	fmt.Println("Parsing package index...")
	allPackages, err := common.ParsePackageIndexFile(indexFilePath)
	if err != nil {
		return fmt.Errorf("failed to parse package index: %w", err)
	}

	fmt.Printf("Found %d packages in package index\n", len(allPackages))

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

	pkgByName := make(map[string]*meta.PackageInfo, len(selectedPackages))
	for _, pkg := range selectedPackages {
		pkgByName[pkg.Name] = pkg
	}

	// change working directory
	chdirErr := os.Chdir(repo)
	if chdirErr != nil {
		return chdirErr
	}

	if err := c.buildPackageDAG(repo, arch, jobs, selectedPackages, buildOrder, pkgByName); err != nil {
		return err
	}

	fmt.Printf("Package(s) Build Success. Output Dir: '%s/dist.%s.*'\n", repo, arch)

	return nil
}

func (c *Client) buildPackageDAG(repo, arch string, jobs int, selectedPackages []*meta.PackageInfo, buildOrder []string, pkgByName map[string]*meta.PackageInfo) error {
	if len(buildOrder) == 0 {
		return nil
	}
	if jobs > len(buildOrder) {
		jobs = len(buildOrder)
	}

	depsByName, dependentsByName, remainingDeps := buildDependencyMaps(selectedPackages)
	orderIndex := make(map[string]int, len(buildOrder))
	for i, name := range buildOrder {
		orderIndex[name] = i
	}

	logRoot := filepath.Join(repo, ".ohloha", "logs")
	resolvedDepsRoot := filepath.Join(repo, ".ohloha", "resolved-deps")
	if err := os.MkdirAll(logRoot, 0o755); err != nil {
		return fmt.Errorf("failed to create log dir: %w", err)
	}
	if err := os.MkdirAll(resolvedDepsRoot, 0o755); err != nil {
		return fmt.Errorf("failed to create resolved deps dir: %w", err)
	}

	ready := make([]string, 0)
	for _, name := range buildOrder {
		if remainingDeps[name] == 0 {
			ready = append(ready, name)
		}
	}

	status := make(map[string]string, len(buildOrder))
	for _, name := range buildOrder {
		status[name] = "pending"
	}
	artifactByName := make(map[string]string, len(buildOrder))

	builderPath := filepath.Join(repo, "builder.sh")
	taskCh := make(chan buildTask, jobs)
	resultCh := make(chan buildResult, jobs)
	var wg sync.WaitGroup
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskCh {
				pkg := pkgByName[task.name]
				resultCh <- runBuildWorker(repo, builderPath, arch, pkg, task.depsFile, task.logPath)
			}
		}()
	}

	running := 0
	completed := 0
	failed := 0
	skipped := 0

	sortReady := func() {
		sort.Slice(ready, func(i, j int) bool {
			return orderIndex[ready[i]] < orderIndex[ready[j]]
		})
	}

	skipDependents := func(root string) {}
	skipDependents = func(root string) {
		dependents := append([]string(nil), dependentsByName[root]...)
		sort.Slice(dependents, func(i, j int) bool {
			return orderIndex[dependents[i]] < orderIndex[dependents[j]]
		})
		for _, dep := range dependents {
			if status[dep] != "pending" {
				continue
			}
			status[dep] = "skipped"
			completed++
			skipped++
			fmt.Printf("[skipped] %s (dependency failed: %s)\n", dep, root)
			skipDependents(dep)
		}
	}

	enqueueReady := func() error {
		sortReady()
		for running < jobs && len(ready) > 0 {
			name := ready[0]
			ready = ready[1:]
			if status[name] != "pending" {
				continue
			}
			pkg := pkgByName[name]
			if pkg == nil {
				return fmt.Errorf("internal error: package %s missing from selected package map", name)
			}
			depsFile, err := writeResolvedDepsFile(resolvedDepsRoot, pkg, arch, depsByName[name], artifactByName)
			if err != nil {
				return err
			}
			logPath := buildLogPath(logRoot, pkg, arch)
			status[name] = "running"
			running++
			fmt.Printf("[running] %s %s (log: %s)\n", pkg.Name, pkg.Version, logPath)
			taskCh <- buildTask{name: name, depsFile: depsFile, logPath: logPath}
		}
		return nil
	}

	for completed < len(buildOrder) {
		if err := enqueueReady(); err != nil {
			close(taskCh)
			wg.Wait()
			return err
		}
		if running == 0 {
			break
		}

		result := <-resultCh
		running--
		completed++
		pkg := pkgByName[result.name]
		if result.err != nil {
			status[result.name] = "failed"
			failed++
			fmt.Printf("[failed] %s %s (log: %s): %v\n", pkg.Name, pkg.Version, result.logPath, result.err)
			skipDependents(result.name)
			continue
		}

		status[result.name] = "success"
		artifactByName[result.name] = result.artifactID
		if result.cacheHit {
			fmt.Printf("[cache-hit] %s %s (artifact: %s)\n", pkg.Name, pkg.Version, result.artifactID)
		} else {
			fmt.Printf("[success] %s %s (artifact: %s)\n", pkg.Name, pkg.Version, result.artifactID)
		}

		dependents := append([]string(nil), dependentsByName[result.name]...)
		sort.Slice(dependents, func(i, j int) bool {
			return orderIndex[dependents[i]] < orderIndex[dependents[j]]
		})
		for _, dependent := range dependents {
			if status[dependent] != "pending" {
				continue
			}
			remainingDeps[dependent]--
			if remainingDeps[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}

	close(taskCh)
	wg.Wait()

	if failed > 0 || skipped > 0 {
		return fmt.Errorf("xcompile failed: %d failed, %d skipped", failed, skipped)
	}
	if completed != len(buildOrder) {
		return fmt.Errorf("xcompile did not finish all packages: %d/%d completed", completed, len(buildOrder))
	}
	return nil
}

func buildDependencyMaps(packages []*meta.PackageInfo) (map[string][]string, map[string][]string, map[string]int) {
	selected := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		selected[pkg.Name] = true
	}

	depsByName := make(map[string][]string, len(packages))
	dependentsByName := make(map[string][]string, len(packages))
	remainingDeps := make(map[string]int, len(packages))
	for _, pkg := range packages {
		depSet := make(map[string]bool)
		for _, dep := range pkg.Depends {
			depName := common.NormalizeDependency(dep)
			if selected[depName] {
				depSet[depName] = true
			}
		}
		for _, dep := range pkg.BuildDepends {
			depName := common.NormalizeDependency(dep)
			if selected[depName] {
				depSet[depName] = true
			}
		}

		deps := make([]string, 0, len(depSet))
		for dep := range depSet {
			deps = append(deps, dep)
		}
		sort.Strings(deps)
		depsByName[pkg.Name] = deps
		remainingDeps[pkg.Name] = len(deps)
		for _, dep := range deps {
			dependentsByName[dep] = append(dependentsByName[dep], pkg.Name)
		}
	}
	return depsByName, dependentsByName, remainingDeps
}

func runBuildWorker(repo, builderPath, arch string, pkg *meta.PackageInfo, depsFile, logPath string) buildResult {
	result := buildResult{name: pkg.Name, logPath: logPath}
	logFile, err := os.Create(logPath)
	if err != nil {
		result.err = fmt.Errorf("failed to create log file: %w", err)
		return result
	}
	defer logFile.Close()

	buildFile := filepath.Join(repo, pkg.BuildFile)
	cmd := exec.Command(builderPath, "--build-one", fmt.Sprintf("--cpu=%s", arch), fmt.Sprintf("--resolved-deps=%s", depsFile), buildFile)
	cmd.Dir = repo
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Run(); err != nil {
		result.err = err
		return result
	}

	cacheKey, err := readBuildID(repo, builderPath, arch, depsFile, buildFile)
	if err != nil {
		result.err = err
		return result
	}
	result.artifactID = cacheKey

	if content, err := os.ReadFile(logPath); err == nil {
		result.cacheHit = strings.Contains(string(content), "cache hit:")
	}
	return result
}

func readBuildID(repo, builderPath, arch, depsFile, buildFile string) (string, error) {
	cmd := exec.Command(builderPath, "--cache-key", fmt.Sprintf("--cpu=%s", arch), fmt.Sprintf("--resolved-deps=%s", depsFile), buildFile)
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to read build id: %w; output: %s", err, strings.TrimSpace(string(output)))
	}
	var payload struct {
		BuildID string `json:"build_id"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return "", fmt.Errorf("failed to parse build id JSON: %w", err)
	}
	if payload.BuildID == "" {
		return "", fmt.Errorf("builder cache-key returned empty build_id")
	}
	return payload.BuildID, nil
}

func writeResolvedDepsFile(root string, pkg *meta.PackageInfo, arch string, deps []string, artifactByName map[string]string) (string, error) {
	dependencyArtifacts := make(map[string]string, len(deps))
	for _, dep := range deps {
		artifactID := artifactByName[dep]
		if artifactID == "" {
			return "", fmt.Errorf("internal error: dependency artifact for %s -> %s is not available", pkg.Name, dep)
		}
		dependencyArtifacts[dep] = artifactID
	}

	path := filepath.Join(root, fmt.Sprintf("%s-%s-%s.json", safePathComponent(pkg.Name), safePathComponent(pkg.Version), safePathComponent(arch)))
	tmpPath := path + ".tmp"
	data, err := json.MarshalIndent(map[string]map[string]string{
		"dependency_artifacts": dependencyArtifacts,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return path, nil
}

func buildLogPath(root string, pkg *meta.PackageInfo, arch string) string {
	return filepath.Join(root, fmt.Sprintf("%s-%s-%s.log", safePathComponent(pkg.Name), safePathComponent(pkg.Version), safePathComponent(arch)))
}

func safePathComponent(value string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_", "\t", "_", "\n", "_")
	return replacer.Replace(value)
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
			return fmt.Errorf("package not found in package index: %s", name)
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
