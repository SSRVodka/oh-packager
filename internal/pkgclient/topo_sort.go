package pkgclient

import (
	"fmt"
	"sort"
	"strings"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/pkg/meta"
)

// BuildNode represents one concrete package version in the build graph.
type BuildNode struct {
	Info         *meta.PackageInfo
	Dependencies []meta.PackageID
}

// TopologicalSort performs topological sort on package dependencies.
func TopologicalSort(packages []*meta.PackageInfo) ([]meta.PackageID, error) {
	graph := make(map[meta.PackageID]*BuildNode)
	inDegree := make(map[meta.PackageID]int)
	byName := make(map[string]meta.PackageID)

	for _, pkg := range packages {
		id := pkg.ID()
		if _, exists := graph[id]; exists {
			return nil, fmt.Errorf("duplicate package: %s", id)
		}
		if existing, exists := byName[pkg.Name]; exists && existing != id {
			return nil, fmt.Errorf("multiple versions of %s selected in one build closure: %s and %s", pkg.Name, existing, id)
		}
		byName[pkg.Name] = id
		graph[id] = &BuildNode{Info: pkg}
		inDegree[id] = 0
	}

	for _, pkg := range packages {
		id := pkg.ID()
		depSet := make(map[meta.PackageID]bool)
		for _, dep := range pkg.Depends {
			depName := dependencyName(dep)
			if depID, exists := byName[depName]; exists {
				depSet[depID] = true
			}
		}
		for _, dep := range pkg.BuildDepends {
			depName := dependencyName(dep)
			if depID, exists := byName[depName]; exists {
				depSet[depID] = true
			}
		}
		for depID := range depSet {
			graph[id].Dependencies = append(graph[id].Dependencies, depID)
			inDegree[id]++
		}
		sortPackageIDs(graph[id].Dependencies)
	}

	queue := make([]meta.PackageID, 0)
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	sortPackageIDs(queue)

	result := make([]meta.PackageID, 0, len(packages))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)

		dependents := make([]meta.PackageID, 0)
		for id, node := range graph {
			for _, dep := range node.Dependencies {
				if dep == current {
					dependents = append(dependents, id)
					break
				}
			}
		}
		sortPackageIDs(dependents)
		for _, id := range dependents {
			inDegree[id]--
			if inDegree[id] == 0 {
				queue = append(queue, id)
			}
		}
		sortPackageIDs(queue)
	}

	if len(result) != len(packages) {
		cycle := findCycle(graph, inDegree)
		if len(cycle) > 0 {
			return nil, fmt.Errorf("circular dependency detected:\n%s", formatCycle(cycle, graph))
		}
		return nil, fmt.Errorf("dependency cycle detected (unable to determine exact path)")
	}

	return result, nil
}

func sortPackageIDs(ids []meta.PackageID) {
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].Name != ids[j].Name {
			return ids[i].Name < ids[j].Name
		}
		return ids[i].Version < ids[j].Version
	})
}

func findCycle(graph map[meta.PackageID]*BuildNode, inDegree map[meta.PackageID]int) []meta.PackageID {
	remaining := make(map[meta.PackageID]bool)
	for id, degree := range inDegree {
		if degree > 0 {
			remaining[id] = true
		}
	}
	if len(remaining) == 0 {
		return nil
	}

	visited := make(map[meta.PackageID]bool)
	recStack := make(map[meta.PackageID]bool)
	parent := make(map[meta.PackageID]meta.PackageID)

	var dfs func(meta.PackageID) []meta.PackageID
	dfs = func(id meta.PackageID) []meta.PackageID {
		visited[id] = true
		recStack[id] = true

		for _, dep := range graph[id].Dependencies {
			if !remaining[dep] {
				continue
			}
			if !visited[dep] {
				parent[dep] = id
				if cycle := dfs(dep); cycle != nil {
					return cycle
				}
			} else if recStack[dep] {
				cycle := []meta.PackageID{dep}
				current := id
				for current != dep {
					cycle = append(cycle, current)
					current = parent[current]
				}
				cycle = append(cycle, dep)
				for i, j := 0, len(cycle)-1; i < j; i, j = i+1, j-1 {
					cycle[i], cycle[j] = cycle[j], cycle[i]
				}
				return cycle
			}
		}

		recStack[id] = false
		return nil
	}

	ids := make([]meta.PackageID, 0, len(remaining))
	for id := range remaining {
		ids = append(ids, id)
	}
	sortPackageIDs(ids)
	for _, id := range ids {
		if !visited[id] {
			if cycle := dfs(id); cycle != nil {
				return cycle
			}
		}
	}
	return nil
}

func formatCycle(cycle []meta.PackageID, graph map[meta.PackageID]*BuildNode) string {
	var sb strings.Builder

	for i := 0; i < len(cycle)-1; i++ {
		current := cycle[i]
		next := cycle[i+1]
		pkg := graph[current].Info

		sb.WriteString(fmt.Sprintf("  %s (%s)\n", current.Name, current.Version))

		var depType []string
		for _, dep := range pkg.Depends {
			if dependencyName(dep) == next.Name {
				depType = append(depType, fmt.Sprintf("runtime: %s", dep))
			}
		}
		for _, dep := range pkg.BuildDepends {
			if dependencyName(dep) == next.Name {
				depType = append(depType, fmt.Sprintf("build: %s", dep))
			}
		}
		sb.WriteString(fmt.Sprintf("    -> depends on [%s]\n", strings.Join(depType, ", ")))
	}

	last := cycle[len(cycle)-1]
	lastPkg := graph[last].Info
	sb.WriteString(fmt.Sprintf("  %s (%s) [cycle closes here]\n", last.Name, lastPkg.Version))
	return sb.String()
}

func PrintDependencyGraph(packages []*meta.PackageInfo, order []meta.PackageID) {
	fmt.Print("\n=== Dependency Graph (Topological Order) ===\n\n")

	pkgMap := make(map[meta.PackageID]*meta.PackageInfo)
	for _, pkg := range packages {
		pkgMap[pkg.ID()] = pkg
	}

	for i, id := range order {
		pkg := pkgMap[id]
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

func dependencyName(dep string) string {
	name, _, err := parseDependencySpec(dep)
	if err == nil {
		return name
	}
	return strings.TrimSpace(common.NormalizeDependency(dep))
}
