package pkgclient

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SSRVodka/oh-packager/internal/common"
	"github.com/SSRVodka/oh-packager/pkg/meta"
)

func TestSelectPackagesWithVersionConstraints(t *testing.T) {
	client := &Client{}
	packages := []*meta.PackageInfo{
		{Name: "libfoo", Version: "1.0.0", BuildFile: "libfoo/BUILD"},
		{Name: "libfoo", Version: "2.0.0", BuildFile: "libfoo/versions/2.0.0/BUILD"},
		{Name: "consumer", Version: "1.0.0", BuildFile: "consumer/BUILD", Depends: []string{"libfoo>=2,<3"}},
	}

	selected, err := client.selectPackagesWithDeps(packages, []string{"consumer"})
	if err != nil {
		t.Fatalf("selectPackagesWithDeps failed: %v", err)
	}

	versions := selectedVersions(selected)
	if versions["consumer"] != "1.0.0" {
		t.Fatalf("consumer not selected: %#v", versions)
	}
	if versions["libfoo"] != "2.0.0" {
		t.Fatalf("libfoo resolved to %q, want 2.0.0", versions["libfoo"])
	}
}

func TestSelectPackagesExactVersionRequest(t *testing.T) {
	client := &Client{}
	packages := []*meta.PackageInfo{
		{Name: "libfoo", Version: "1.0.0", BuildFile: "libfoo/BUILD"},
		{Name: "libfoo", Version: "2.0.0", BuildFile: "libfoo/versions/2.0.0/BUILD"},
	}

	selected, err := client.selectPackagesWithDeps(packages, []string{"libfoo==1.0.0"})
	if err != nil {
		t.Fatalf("selectPackagesWithDeps failed: %v", err)
	}

	versions := selectedVersions(selected)
	if versions["libfoo"] != "1.0.0" {
		t.Fatalf("libfoo resolved to %q, want 1.0.0", versions["libfoo"])
	}
}

func TestSelectPackagesReportsConstraintConflict(t *testing.T) {
	client := &Client{}
	packages := []*meta.PackageInfo{
		{Name: "libfoo", Version: "1.0.0", BuildFile: "libfoo/BUILD"},
		{Name: "libfoo", Version: "2.0.0", BuildFile: "libfoo/versions/2.0.0/BUILD"},
		{Name: "consumer", Version: "1.0.0", BuildFile: "consumer/BUILD", Depends: []string{"libfoo>=2"}},
	}

	_, err := client.selectPackagesWithDeps(packages, []string{"consumer", "libfoo<2"})
	if err == nil {
		t.Fatal("selectPackagesWithDeps unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "libfoo") {
		t.Fatalf("conflict error does not name libfoo: %v", err)
	}
}

func TestSelectPackagesReportsMissingPackageOnMultipleLines(t *testing.T) {
	client := &Client{}
	packages := []*meta.PackageInfo{
		{Name: "consumer", Version: "1.0.0", BuildFile: "consumer/BUILD", Depends: []string{"missing>=2"}},
	}

	_, err := client.selectPackagesWithDeps(packages, []string{"consumer"})
	if err == nil {
		t.Fatal("selectPackagesWithDeps unexpectedly succeeded")
	}
	msg := err.Error()
	for _, want := range []string{
		"cannot resolve consumer",
		"required by:",
		"blocked by:",
		"cannot resolve missing",
		"reason: package not found in package index",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error does not contain %q:\n%s", want, msg)
		}
	}
	if !strings.Contains(msg, "\n") {
		t.Fatalf("error is not multi-line: %s", msg)
	}
}

func TestBuildLogPathSeparatesPackageVersions(t *testing.T) {
	path := buildLogPath("/repo/.ohloha/logs", &meta.PackageInfo{Name: "openssl", Version: "3.5.0"}, "aarch64")
	want := "/repo/.ohloha/logs/aarch64/openssl/3.5.0/build.log"
	if path != want {
		t.Fatalf("buildLogPath() = %q, want %q", path, want)
	}
}

func TestRealPackageIndexResolvesNumpyMajorVersions(t *testing.T) {
	repo := filepath.Join("..", "..", "ohloha_pkgs")
	indexPath := filepath.Join(repo, "PKG_INDEX.json")
	if _, err := os.Stat(indexPath); err != nil {
		if !os.IsNotExist(err) {
			t.Fatalf("stat package index failed: %v", err)
		}
		cmd := exec.Command("bash", "./gen-pkg-index.sh")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("gen-pkg-index.sh failed: %v\n%s", err, out)
		}
		t.Cleanup(func() { _ = os.Remove(indexPath) })
	}

	packages, err := common.ParsePackageIndexFile(indexPath)
	if err != nil {
		t.Fatalf("ParsePackageIndexFile failed: %v", err)
	}

	client := &Client{}
	tests := []struct {
		request string
		want    string
	}{
		{request: "python3-opencv", want: "2.3.1"},
		{request: "opencv", want: "2.3.1"},
		{request: "onnxruntime", want: "1.26.5"},
	}

	for _, tt := range tests {
		selected, err := client.selectPackagesWithDeps(packages, []string{tt.request})
		if err != nil {
			t.Fatalf("selectPackagesWithDeps(%q) failed: %v", tt.request, err)
		}
		versions := selectedVersions(selected)
		if versions["python3-numpy"] != tt.want {
			t.Fatalf("selectPackagesWithDeps(%q) resolved python3-numpy %q, want %q", tt.request, versions["python3-numpy"], tt.want)
		}
	}
}

func selectedVersions(packages []*meta.PackageInfo) map[string]string {
	versions := make(map[string]string, len(packages))
	for _, pkg := range packages {
		versions[pkg.Name] = pkg.Version
	}
	return versions
}
