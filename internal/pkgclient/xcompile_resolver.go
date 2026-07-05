package pkgclient

import (
	"fmt"
	"sort"
	"strings"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/pkg/meta"
	"github.com/blang/semver/v4"
)

type packageRequirement struct {
	Constraint common.Constraint
	Source     string
}

type packageResolver struct {
	byName map[string][]*meta.PackageInfo
}

func (c *Client) selectPackagesWithDeps(allPackages []*meta.PackageInfo, requested []string) ([]*meta.PackageInfo, error) {
	resolver, err := newPackageResolver(allPackages)
	if err != nil {
		return nil, err
	}

	requirements := make(map[string][]packageRequirement)
	for _, req := range requested {
		req = strings.TrimSpace(req)
		if req == "" {
			continue
		}
		name, constraints, err := parseDependencySpec(req)
		if err != nil {
			return nil, fmt.Errorf("invalid package request %q: %w", req, err)
		}
		for _, constraint := range constraints {
			addRequirement(requirements, name, constraint, "request "+req)
		}
	}

	selected, err := resolver.solve(requirements, map[string]*meta.PackageInfo{})
	if err != nil {
		return nil, err
	}

	result := make([]*meta.PackageInfo, 0, len(selected))
	for _, pkg := range selected {
		result = append(result, pkg)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return compareVersions(result[i].Version, result[j].Version) < 0
	})
	return result, nil
}

func newPackageResolver(packages []*meta.PackageInfo) (*packageResolver, error) {
	byID := make(map[meta.PackageID]bool, len(packages))
	byName := make(map[string][]*meta.PackageInfo)
	for _, pkg := range packages {
		if pkg == nil {
			return nil, fmt.Errorf("package index contains null package")
		}
		id := pkg.ID()
		if byID[id] {
			return nil, fmt.Errorf("duplicate package in package index: %s", id)
		}
		byID[id] = true
		byName[pkg.Name] = append(byName[pkg.Name], pkg)
	}

	for name := range byName {
		sort.SliceStable(byName[name], func(i, j int) bool {
			return compareVersions(byName[name][i].Version, byName[name][j].Version) > 0
		})
	}
	return &packageResolver{byName: byName}, nil
}

func (r *packageResolver) solve(requirements map[string][]packageRequirement, selected map[string]*meta.PackageInfo) (map[string]*meta.PackageInfo, error) {
	name := nextUnresolvedRequirement(requirements, selected)
	if name == "" {
		return selected, nil
	}

	candidates := r.byName[name]
	if len(candidates) == 0 {
		return nil, fmt.Errorf("package not found in package index: %s", name)
	}

	constraints := constraintsOnly(requirements[name])
	var lastErr error
	for _, candidate := range candidates {
		if !common.SatisfiesConstraints(candidate.Version, constraints) {
			continue
		}

		nextRequirements := cloneRequirements(requirements)
		nextSelected := cloneSelected(selected)
		nextSelected[name] = candidate

		if err := addPackageDependencyRequirements(nextRequirements, candidate); err != nil {
			return nil, err
		}
		if conflictName := selectedConflict(nextRequirements, nextSelected); conflictName != "" {
			lastErr = fmt.Errorf("selected %s does not satisfy %s", nextSelected[conflictName].ID(), formatRequirements(conflictName, nextRequirements[conflictName]))
			continue
		}

		resolved, err := r.solve(nextRequirements, nextSelected)
		if err == nil {
			return resolved, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return nil, fmt.Errorf("cannot resolve %s with %s: %w", name, formatRequirements(name, requirements[name]), lastErr)
	}
	return nil, fmt.Errorf("no version of %s satisfies %s", name, formatRequirements(name, requirements[name]))
}

func nextUnresolvedRequirement(requirements map[string][]packageRequirement, selected map[string]*meta.PackageInfo) string {
	names := make([]string, 0, len(requirements))
	for name, reqs := range requirements {
		if len(reqs) == 0 {
			continue
		}
		pkg := selected[name]
		if pkg == nil || !common.SatisfiesConstraints(pkg.Version, constraintsOnly(reqs)) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func addPackageDependencyRequirements(requirements map[string][]packageRequirement, pkg *meta.PackageInfo) error {
	for _, dep := range pkg.Depends {
		if err := addDependencyRequirements(requirements, dep, pkg.ID().String()+" runtime dep"); err != nil {
			return err
		}
	}
	for _, dep := range pkg.BuildDepends {
		if err := addDependencyRequirements(requirements, dep, pkg.ID().String()+" build dep"); err != nil {
			return err
		}
	}
	return nil
}

func addDependencyRequirements(requirements map[string][]packageRequirement, dep, source string) error {
	name, constraints, err := parseDependencySpec(dep)
	if err != nil {
		return fmt.Errorf("invalid dependency %q from %s: %w", dep, source, err)
	}
	for _, constraint := range constraints {
		addRequirement(requirements, name, constraint, source+" "+dep)
	}
	return nil
}

func parseDependencySpec(spec string) (string, []common.Constraint, error) {
	parts := strings.Split(spec, ",")
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("empty dependency")
	}

	first := strings.TrimSpace(parts[0])
	name, firstConstraint, err := common.ParseDep(first)
	if err != nil {
		return "", nil, err
	}

	constraints := []common.Constraint{firstConstraint}
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		dep := part
		if strings.HasPrefix(part, ">=") || strings.HasPrefix(part, "<=") || strings.HasPrefix(part, "==") || strings.HasPrefix(part, ">") || strings.HasPrefix(part, "<") {
			dep = name + part
		}
		partName, constraint, err := common.ParseDep(dep)
		if err != nil {
			return "", nil, err
		}
		if partName != name {
			return "", nil, fmt.Errorf("mixed package names in dependency expression: %s and %s", name, partName)
		}
		constraints = append(constraints, constraint)
	}
	return name, constraints, nil
}

func addRequirement(requirements map[string][]packageRequirement, name string, constraint common.Constraint, source string) {
	req := packageRequirement{Constraint: constraint, Source: source}
	for _, existing := range requirements[name] {
		if existing == req {
			return
		}
	}
	requirements[name] = append(requirements[name], req)
}

func selectedConflict(requirements map[string][]packageRequirement, selected map[string]*meta.PackageInfo) string {
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !common.SatisfiesConstraints(selected[name].Version, constraintsOnly(requirements[name])) {
			return name
		}
	}
	return ""
}

func constraintsOnly(reqs []packageRequirement) []common.Constraint {
	constraints := make([]common.Constraint, 0, len(reqs))
	for _, req := range reqs {
		constraints = append(constraints, req.Constraint)
	}
	return constraints
}

func cloneRequirements(in map[string][]packageRequirement) map[string][]packageRequirement {
	out := make(map[string][]packageRequirement, len(in))
	for name, reqs := range in {
		out[name] = append([]packageRequirement(nil), reqs...)
	}
	return out
}

func cloneSelected(in map[string]*meta.PackageInfo) map[string]*meta.PackageInfo {
	out := make(map[string]*meta.PackageInfo, len(in))
	for name, pkg := range in {
		out[name] = pkg
	}
	return out
}

func formatRequirements(name string, reqs []packageRequirement) string {
	if len(reqs) == 0 {
		return "(no constraints)"
	}
	parts := make([]string, 0, len(reqs))
	for _, req := range reqs {
		constraint := req.Constraint.Op + req.Constraint.Ver
		if req.Constraint.Op == "" {
			constraint = "any"
		}
		parts = append(parts, fmt.Sprintf("%s from %s", constraint, req.Source))
	}
	sort.Strings(parts)
	return fmt.Sprintf("%s constraints [%s]", name, strings.Join(parts, "; "))
}

func compareVersions(a, b string) int {
	av, aErr := semver.ParseTolerant(a)
	bv, bErr := semver.ParseTolerant(b)
	switch {
	case aErr == nil && bErr == nil:
		if av.GT(bv) {
			return 1
		}
		if av.LT(bv) {
			return -1
		}
		return 0
	case aErr == nil:
		return 1
	case bErr == nil:
		return -1
	default:
		return strings.Compare(a, b)
	}
}
