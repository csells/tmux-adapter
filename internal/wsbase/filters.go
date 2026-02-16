package wsbase

import (
	"fmt"
	"regexp"
)

// CompileSessionFilters compiles optional include/exclude regex strings.
// Returns nil for empty strings (no filter). Returns error for invalid regex.
func CompileSessionFilters(includeStr, excludeStr string) (*regexp.Regexp, *regexp.Regexp, error) {
	var include, exclude *regexp.Regexp
	if includeStr != "" {
		var err error
		include, err = regexp.Compile(includeStr)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid includeSessionFilter: %v", err)
		}
	}
	if excludeStr != "" {
		var err error
		exclude, err = regexp.Compile(excludeStr)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid excludeSessionFilter: %v", err)
		}
	}
	return include, exclude, nil
}

// PassesFilter checks if a session name passes the include/exclude regex filters.
func PassesFilter(name string, include, exclude *regexp.Regexp) bool {
	if include != nil && !include.MatchString(name) {
		return false
	}
	if exclude != nil && exclude.MatchString(name) {
		return false
	}
	return true
}
