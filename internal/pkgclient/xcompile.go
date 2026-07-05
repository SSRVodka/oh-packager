package pkgclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/pkg/meta"
)

type buildTask struct {
	id       meta.PackageID
	depsFile string
	logPath  string
}

type buildResult struct {
	id         meta.PackageID
	artifactID string
	logPath    string
	cacheHit   bool
	err        error
}

// XCompile builds packages from source in topological order.
func (c *Client) XCompile(packageNames []string, arch string, jobs int, keepGoing bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if c.Config.PkgSrcRepo == "" {
		return fmt.Errorf("package source repository for cross compile not configured")
	}
	if c.Config.OhosSdk == "" {
		return fmt.Errorf("OHOS SDK path not configured")
	}
	if _, err := common.LoadLocalSdkInfo(c.Config.OhosSdk); err != nil {
		return err
	}
	if jobs < 1 {
		return fmt.Errorf("jobs must be >= 1")
	}

	fmt.Printf("Cross-compiling for architecture: %s\n", arch)
	fmt.Printf("Build jobs: %d\n", jobs)
	fmt.Printf("Keep going after failures: %t\n", keepGoing)
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

	pkgByID := make(map[meta.PackageID]*meta.PackageInfo, len(selectedPackages))
	for _, pkg := range selectedPackages {
		pkgByID[pkg.ID()] = pkg
	}

	// change working directory
	chdirErr := os.Chdir(repo)
	if chdirErr != nil {
		return chdirErr
	}

	if err := c.buildPackageDAG(ctx, repo, c.Config.OhosSdk, arch, jobs, keepGoing, selectedPackages, buildOrder, pkgByID); err != nil {
		return err
	}

	fmt.Printf("Package(s) Build Success. Output Dir: '%s/dist.%s.*'\n", repo, arch)

	return nil
}

func (c *Client) buildPackageDAG(ctx context.Context, repo, ohosSdk, arch string, jobs int, keepGoing bool, selectedPackages []*meta.PackageInfo, buildOrder []meta.PackageID, pkgByID map[meta.PackageID]*meta.PackageInfo) error {
	if len(buildOrder) == 0 {
		return nil
	}
	if jobs > len(buildOrder) {
		jobs = len(buildOrder)
	}

	depsByID, dependentsByID, remainingDeps := buildDependencyMaps(selectedPackages)
	orderIndex := make(map[meta.PackageID]int, len(buildOrder))
	for i, id := range buildOrder {
		orderIndex[id] = i
	}

	logRoot := filepath.Join(repo, ".ohloha", "logs")
	resolvedDepsRoot := filepath.Join(repo, ".ohloha", "resolved-deps")
	if err := os.MkdirAll(logRoot, 0o755); err != nil {
		return fmt.Errorf("failed to create log dir: %w", err)
	}
	if err := os.MkdirAll(resolvedDepsRoot, 0o755); err != nil {
		return fmt.Errorf("failed to create resolved deps dir: %w", err)
	}

	ready := make([]meta.PackageID, 0)
	for _, id := range buildOrder {
		if remainingDeps[id] == 0 {
			ready = append(ready, id)
		}
	}

	status := make(map[meta.PackageID]string, len(buildOrder))
	for _, id := range buildOrder {
		status[id] = "pending"
	}
	artifactByID := make(map[meta.PackageID]string, len(buildOrder))

	builderPath := filepath.Join(repo, "builder.sh")
	taskCh := make(chan buildTask, jobs)
	resultCh := make(chan buildResult, jobs)
	var wg sync.WaitGroup
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskCh {
				pkg := pkgByID[task.id]
				resultCh <- runBuildWorker(ctx, repo, builderPath, ohosSdk, arch, pkg, task.depsFile, task.logPath)
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

	skipDependents := func(root meta.PackageID) {}
	skipDependents = func(root meta.PackageID) {
		dependents := append([]meta.PackageID(nil), dependentsByID[root]...)
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

	skipPending := func(reason string) {
		for _, id := range buildOrder {
			if status[id] != "pending" {
				continue
			}
			status[id] = "skipped"
			completed++
			skipped++
			fmt.Printf("[skipped] %s (%s)\n", id, reason)
		}
	}

	enqueueReady := func() error {
		sortReady()
		for running < jobs && len(ready) > 0 {
			id := ready[0]
			ready = ready[1:]
			if status[id] != "pending" {
				continue
			}
			pkg := pkgByID[id]
			if pkg == nil {
				return fmt.Errorf("internal error: package %s missing from selected package map", id)
			}
			depsFile, err := writeResolvedDepsFile(repo, resolvedDepsRoot, pkg, arch, depsByID[id], pkgByID, artifactByID)
			if err != nil {
				return err
			}
			logPath := buildLogPath(logRoot, pkg, arch)
			status[id] = "running"
			running++
			fmt.Printf("[running] %s %s (log: %s)\n", pkg.Name, pkg.Version, logPath)
			select {
			case taskCh <- buildTask{id: id, depsFile: depsFile, logPath: logPath}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}

	taskChClosed := false
	closeTasks := func() {
		if !taskChClosed {
			close(taskCh)
			taskChClosed = true
		}
	}

	for completed < len(buildOrder) {
		if err := enqueueReady(); err != nil {
			closeTasks()
			wg.Wait()
			return err
		}
		if running == 0 {
			break
		}

		var result buildResult
		select {
		case result = <-resultCh:
		case <-ctx.Done():
			closeTasks()
			wg.Wait()
			return fmt.Errorf("xcompile canceled: %w", ctx.Err())
		}
		running--
		completed++
		pkg := pkgByID[result.id]
		if result.err != nil {
			status[result.id] = "failed"
			failed++
			fmt.Printf("[failed] %s %s (log: %s): %v\n", pkg.Name, pkg.Version, result.logPath, result.err)
			skipDependents(result.id)
			if !keepGoing {
				skipPending("build stopped after failure")
			}
			continue
		}

		status[result.id] = "success"
		artifactByID[result.id] = result.artifactID
		if result.cacheHit {
			fmt.Printf("[cache-hit] %s %s (artifact: %s)\n", pkg.Name, pkg.Version, result.artifactID)
		} else {
			fmt.Printf("[success] %s %s (artifact: %s)\n", pkg.Name, pkg.Version, result.artifactID)
		}

		dependents := append([]meta.PackageID(nil), dependentsByID[result.id]...)
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

	closeTasks()
	wg.Wait()

	if failed > 0 || skipped > 0 {
		return fmt.Errorf("xcompile failed: %d failed, %d skipped", failed, skipped)
	}
	if completed != len(buildOrder) {
		return fmt.Errorf("xcompile did not finish all packages: %d/%d completed", completed, len(buildOrder))
	}
	return nil
}

func buildDependencyMaps(packages []*meta.PackageInfo) (map[meta.PackageID][]meta.PackageID, map[meta.PackageID][]meta.PackageID, map[meta.PackageID]int) {
	selected := make(map[string]meta.PackageID, len(packages))
	for _, pkg := range packages {
		selected[pkg.Name] = pkg.ID()
	}

	depsByID := make(map[meta.PackageID][]meta.PackageID, len(packages))
	dependentsByID := make(map[meta.PackageID][]meta.PackageID, len(packages))
	remainingDeps := make(map[meta.PackageID]int, len(packages))
	for _, pkg := range packages {
		id := pkg.ID()
		depSet := make(map[meta.PackageID]bool)
		for _, dep := range pkg.Depends {
			depName := dependencyName(dep)
			if depID, exists := selected[depName]; exists {
				depSet[depID] = true
			}
		}
		for _, dep := range pkg.BuildDepends {
			depName := dependencyName(dep)
			if depID, exists := selected[depName]; exists {
				depSet[depID] = true
			}
		}

		deps := make([]meta.PackageID, 0, len(depSet))
		for dep := range depSet {
			deps = append(deps, dep)
		}
		sortPackageIDs(deps)
		depsByID[id] = deps
		remainingDeps[id] = len(deps)
		for _, dep := range deps {
			dependentsByID[dep] = append(dependentsByID[dep], id)
		}
	}
	return depsByID, dependentsByID, remainingDeps
}

func runBuildWorker(ctx context.Context, repo, builderPath, ohosSdk, arch string, pkg *meta.PackageInfo, depsFile, logPath string) buildResult {
	result := buildResult{id: pkg.ID(), logPath: logPath}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		result.err = fmt.Errorf("failed to create log dir: %w", err)
		return result
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		result.err = fmt.Errorf("failed to create log file: %w", err)
		return result
	}
	defer logFile.Close()

	buildFile := filepath.Join(repo, pkg.BuildFile)
	cmd := exec.CommandContext(ctx, builderPath, "--build-one", fmt.Sprintf("--cpu=%s", arch), fmt.Sprintf("--resolved-deps=%s", depsFile), buildFile)
	cmd.Dir = repo
	cmd.Env = buildWorkerEnv(ohosSdk)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 10 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		pgid := -cmd.Process.Pid
		if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			return err
		}
		go func() {
			time.Sleep(5 * time.Second)
			_ = syscall.Kill(pgid, syscall.SIGKILL)
		}()
		return nil
	}
	if err := cmd.Run(); err != nil {
		result.err = err
		return result
	}

	cacheKey, err := readBuildID(repo, builderPath, ohosSdk, arch, depsFile, buildFile)
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

func readBuildID(repo, builderPath, ohosSdk, arch, depsFile, buildFile string) (string, error) {
	cmd := exec.Command(builderPath, "--cache-key", fmt.Sprintf("--cpu=%s", arch), fmt.Sprintf("--resolved-deps=%s", depsFile), buildFile)
	cmd.Dir = repo
	cmd.Env = buildWorkerEnv(ohosSdk)
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

func buildWorkerEnv(ohosSdk string) []string {
	return append(os.Environ(), "OHOS_SDK="+ohosSdk)
}

func writeResolvedDepsFile(repo, root string, pkg *meta.PackageInfo, arch string, deps []meta.PackageID, pkgByID map[meta.PackageID]*meta.PackageInfo, artifactByID map[meta.PackageID]string) (string, error) {
	type resolvedDependency struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		ArtifactID string `json:"artifact_id"`
		Path       string `json:"path"`
	}

	dependencyArtifacts := make(map[string]resolvedDependency, len(deps))
	dependencyPaths := make(map[string]string, len(deps))
	dependencyList := make([]resolvedDependency, 0, len(deps))
	for _, dep := range deps {
		depPkg := pkgByID[dep]
		if depPkg == nil {
			return "", fmt.Errorf("internal error: dependency package %s for %s is not available", dep, pkg.ID())
		}
		artifactID := artifactByID[dep]
		if artifactID == "" {
			return "", fmt.Errorf("internal error: dependency artifact for %s -> %s is not available", pkg.ID(), dep)
		}
		distPath := filepath.Join(repo, fmt.Sprintf("dist.%s.%s-%s", arch, depPkg.Name, depPkg.Version))
		resolved := resolvedDependency{
			Name:       depPkg.Name,
			Version:    depPkg.Version,
			ArtifactID: artifactID,
			Path:       distPath,
		}
		dependencyArtifacts[depPkg.Name] = resolved
		dependencyPaths[depPkg.Name] = distPath
		dependencyList = append(dependencyList, resolved)
	}
	sort.Slice(dependencyList, func(i, j int) bool {
		if dependencyList[i].Name != dependencyList[j].Name {
			return dependencyList[i].Name < dependencyList[j].Name
		}
		return dependencyList[i].Version < dependencyList[j].Version
	})

	path := filepath.Join(root, fmt.Sprintf("%s-%s-%s.json", safePathComponent(pkg.Name), safePathComponent(pkg.Version), safePathComponent(arch)))
	tmpPath := path + ".tmp"
	payload := map[string]any{
		"dependency_artifacts": dependencyArtifacts,
		"dependency_paths":     dependencyPaths,
		"dependencies":         dependencyList,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
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
	return filepath.Join(root, safePathComponent(arch), safePathComponent(pkg.Name), safePathComponent(pkg.Version), "build.log")
}

func safePathComponent(value string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_", "\t", "_", "\n", "_")
	return replacer.Replace(value)
}
