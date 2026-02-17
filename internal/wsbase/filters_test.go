package wsbase

import (
	"regexp"
	"testing"
)

func TestCompileSessionFiltersEmpty(t *testing.T) {
	inc, exc, err := CompileSessionFilters("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inc != nil {
		t.Fatal("expected nil include filter for empty string")
	}
	if exc != nil {
		t.Fatal("expected nil exclude filter for empty string")
	}
}

func TestCompileSessionFiltersValidInclude(t *testing.T) {
	inc, exc, err := CompileSessionFilters("^agent-.*", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inc == nil {
		t.Fatal("expected non-nil include filter")
	}
	if exc != nil {
		t.Fatal("expected nil exclude filter")
	}
	if !inc.MatchString("agent-foo") {
		t.Fatal("expected include filter to match agent-foo")
	}
}

func TestCompileSessionFiltersValidExclude(t *testing.T) {
	inc, exc, err := CompileSessionFilters("", "^tmp-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inc != nil {
		t.Fatal("expected nil include filter")
	}
	if exc == nil {
		t.Fatal("expected non-nil exclude filter")
	}
	if !exc.MatchString("tmp-session") {
		t.Fatal("expected exclude filter to match tmp-session")
	}
}

func TestCompileSessionFiltersBoth(t *testing.T) {
	inc, exc, err := CompileSessionFilters("^agent-", "debug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inc == nil {
		t.Fatal("expected non-nil include filter")
	}
	if exc == nil {
		t.Fatal("expected non-nil exclude filter")
	}
}

func TestCompileSessionFiltersInvalidInclude(t *testing.T) {
	_, _, err := CompileSessionFilters("[invalid", "")
	if err == nil {
		t.Fatal("expected error for invalid include regex")
	}
}

func TestCompileSessionFiltersInvalidExclude(t *testing.T) {
	_, _, err := CompileSessionFilters("", "[invalid")
	if err == nil {
		t.Fatal("expected error for invalid exclude regex")
	}
}

func TestPassesFilterNilFilters(t *testing.T) {
	if !PassesFilter("anything", nil, nil) {
		t.Fatal("nil filters should pass all names")
	}
}

func TestPassesFilterIncludeOnly(t *testing.T) {
	inc := regexp.MustCompile("^agent-")

	if !PassesFilter("agent-foo", inc, nil) {
		t.Fatal("expected agent-foo to pass include filter")
	}
	if PassesFilter("other-session", inc, nil) {
		t.Fatal("expected other-session to fail include filter")
	}
}

func TestPassesFilterExcludeOnly(t *testing.T) {
	exc := regexp.MustCompile("debug")

	if !PassesFilter("agent-foo", nil, exc) {
		t.Fatal("expected agent-foo to pass exclude filter")
	}
	if PassesFilter("agent-debug", nil, exc) {
		t.Fatal("expected agent-debug to be excluded")
	}
}

func TestPassesFilterBoth(t *testing.T) {
	inc := regexp.MustCompile("^agent-")
	exc := regexp.MustCompile("debug")

	if !PassesFilter("agent-foo", inc, exc) {
		t.Fatal("expected agent-foo to pass both filters")
	}
	if PassesFilter("agent-debug", inc, exc) {
		t.Fatal("expected agent-debug to be excluded despite matching include")
	}
	if PassesFilter("other-foo", inc, exc) {
		t.Fatal("expected other-foo to fail include")
	}
}
