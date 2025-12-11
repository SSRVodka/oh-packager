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
		// Find and report the cycle
		cycle := findCycle(graph, inDegree)
		if len(cycle) > 0 {
			return nil, fmt.Errorf("circular dependency detected:\n%s", formatCycle(cycle, graph))
		}
		return nil, fmt.Errorf("dependency cycle detected (unable to determine exact path)")
	}

	return result, nil
}

// findCycle detects a cycle in the dependency graph using DFS
// Returns a slice representing the cycle path
func findCycle(graph map[string]*BuildNode, inDegree map[string]int) []string {
	// Only consider nodes that are part of the cycle (inDegree > 0)
	remaining := make(map[string]bool)
	for name, degree := range inDegree {
		if degree > 0 {
			remaining[name] = true
		}
	}

	if len(remaining) == 0 {
		return nil
	}

	// Use DFS to find a cycle
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	parent := make(map[string]string)

	var dfs func(node string) []string
	dfs = func(node string) []string {
		visited[node] = true
		recStack[node] = true

		for _, dep := range graph[node].Dependencies {
			// Only follow edges to nodes that are part of remaining set
			if !remaining[dep] {
				continue
			}

			if !visited[dep] {
				parent[dep] = node
				if cycle := dfs(dep); cycle != nil {
					return cycle
				}
			} else if recStack[dep] {
				// Found a cycle, reconstruct it
				cycle := []string{dep}
				current := node
				for current != dep {
					cycle = append(cycle, current)
					current = parent[current]
				}
				cycle = append(cycle, dep) // Complete the cycle

				// Reverse to get correct order
				for i, j := 0, len(cycle)-1; i < j; i, j = i+1, j-1 {
					cycle[i], cycle[j] = cycle[j], cycle[i]
				}
				return cycle
			}
		}

		recStack[node] = false
		return nil
	}

	// Start DFS from any remaining node
	for name := range remaining {
		if !visited[name] {
			if cycle := dfs(name); cycle != nil {
				return cycle
			}
		}
	}

	return nil
}

// formatCycle formats the cycle path into a readable error message
func formatCycle(cycle []string, graph map[string]*BuildNode) string {
	var sb strings.Builder

	for i := 0; i < len(cycle)-1; i++ {
		current := cycle[i]
		next := cycle[i+1]

		pkg := graph[current].Info

		sb.WriteString(fmt.Sprintf("  %s (%s)\n", current, pkg.Version))

		// Determine which type of dependency causes the edge
		var depType []string
		for _, dep := range pkg.Depends {
			if common.NormalizeDependency(dep) == next {
				depType = append(depType, fmt.Sprintf("runtime: %s", dep))
			}
		}
		for _, dep := range pkg.BuildDepends {
			if common.NormalizeDependency(dep) == next {
				depType = append(depType, fmt.Sprintf("build: %s", dep))
			}
		}

		sb.WriteString(fmt.Sprintf("    └─> depends on [%s]\n", strings.Join(depType, ", ")))
	}

	// Add the last node that completes the cycle
	lastPkg := graph[cycle[len(cycle)-1]].Info
	sb.WriteString(fmt.Sprintf("  %s (%s) [cycle closes here]\n", cycle[len(cycle)-1], lastPkg.Version))

	return sb.String()
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
