package pkgclient

import (
	"fmt"
	"strings"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/pkg/meta"
)

// BuildNode represents a package in the build graph
type BuildNode struct {
	Info         *meta.PackageInfo
	Dependencies []string // Normalized dependency names (runtime + build)
}

// TopologicalSort performs topological sort on package dependencies
// Returns ordered list of package names or error if cycle detected
func TopologicalSort(packages []*meta.PackageInfo) ([]string, error) {
	// Build adjacency list and in-degree map
	graph := make(map[string]*BuildNode)
	inDegree := make(map[string]int)

	// Initialize graph
	for _, pkg := range packages {
		if _, exists := graph[pkg.Name]; exists {
			return nil, fmt.Errorf("duplicate package: %s", pkg.Name)
		}

		graph[pkg.Name] = &BuildNode{
			Info:         pkg,
			Dependencies: []string{},
		}
		inDegree[pkg.Name] = 0
	}

	// Build edges: collect all dependencies (runtime + build-time)
	for _, pkg := range packages {
		allDeps := make(map[string]bool)

		// Process runtime dependencies
		for _, dep := range pkg.Depends {
			depName := common.NormalizeDependency(dep)
			allDeps[depName] = true
		}

		// Process build-time dependencies
		for _, dep := range pkg.BuildDepends {
			depName := common.NormalizeDependency(dep)
			allDeps[depName] = true
		}

		// Add unique dependencies to graph
		for depName := range allDeps {
			// Only add edge if dependency is in our package set
			if _, exists := graph[depName]; exists {
				graph[pkg.Name].Dependencies = append(graph[pkg.Name].Dependencies, depName)
				inDegree[pkg.Name]++
			}
		}
	}

	// Kahn's algorithm for topological sort
	var queue []string
	for name, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, name)
		}
	}

	var result []string
	for len(queue) > 0 {
		// Dequeue
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)

		// Process all packages that depend on current
		for name, node := range graph {
			for _, dep := range node.Dependencies {
				if dep == current {
					inDegree[name]--
					if inDegree[name] == 0 {
						queue = append(queue, name)
					}
				}
			}
		}
	}

	// Check for cycles
	if len(result) != len(packages) {
		return nil, fmt.Errorf("dependency cycle detected")
	}

	return result, nil
}

// PrintDependencyGraph prints the dependency graph in a readable format
func PrintDependencyGraph(packages []*meta.PackageInfo, order []string) {
	fmt.Println("\n=== Dependency Graph (Topological Order) ===\n")

	// Create lookup map
	pkgMap := make(map[string]*meta.PackageInfo)
	for _, pkg := range packages {
		pkgMap[pkg.Name] = pkg
	}

	for i, name := range order {
		pkg := pkgMap[name]
		fmt.Printf("%d. %s %s\n", i+1, pkg.Name, pkg.Version)

		if len(pkg.Depends) > 0 {
			fmt.Printf("   Runtime deps: %s\n", strings.Join(pkg.Depends, ", "))
		}
		if len(pkg.BuildDepends) > 0 {
			fmt.Printf("   Build deps:   %s\n", strings.Join(pkg.BuildDepends, ", "))
		}
		fmt.Println()
	}

	fmt.Println("=== Build order established ===")
}
