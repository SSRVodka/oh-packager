package common

import "testing"

func TestSplitDependencyCSVPreservesRangeConstraints(t *testing.T) {
	got := SplitDependencyCSV("libfoo>=2,<3,libbar,libbaz==1.0.0")
	want := []string{"libfoo>=2,<3", "libbar", "libbaz==1.0.0"}
	if len(got) != len(want) {
		t.Fatalf("SplitDependencyCSV returned %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SplitDependencyCSV returned %#v, want %#v", got, want)
		}
	}
}

func TestParseDependencySpecRange(t *testing.T) {
	name, constraints, err := ParseDependencySpec("libfoo>=2,<3")
	if err != nil {
		t.Fatalf("ParseDependencySpec failed: %v", err)
	}
	if name != "libfoo" {
		t.Fatalf("name = %q, want libfoo", name)
	}
	if len(constraints) != 2 {
		t.Fatalf("constraints = %#v, want two constraints", constraints)
	}
	if constraints[0].Op != ">=" || constraints[0].Ver != "2" || constraints[1].Op != "<" || constraints[1].Ver != "3" {
		t.Fatalf("constraints = %#v, want >=2 and <3", constraints)
	}
}
