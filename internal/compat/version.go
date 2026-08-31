package compat

import (
	"fmt"
	"strconv"
	"strings"
)

type Version struct {
	Major int
	Minor int
	Patch int
}

func Parse(text string) (Version, error) {
	text = strings.TrimSpace(strings.TrimPrefix(text, "v"))
	if index := strings.IndexAny(text, "+-"); index >= 0 {
		text = text[:index]
	}
	parts := strings.Split(text, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q", text)
	}
	values := [3]int{}
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return Version{}, fmt.Errorf("invalid version %q", text)
		}
		values[index] = value
	}
	return Version{Major: values[0], Minor: values[1], Patch: values[2]}, nil
}

func AtLeast(actual, minimum Version) bool {
	if actual.Major != minimum.Major {
		return actual.Major > minimum.Major
	}
	if actual.Minor != minimum.Minor {
		return actual.Minor > minimum.Minor
	}
	return actual.Patch >= minimum.Patch
}

func Extract(output string) (Version, error) {
	for _, field := range strings.Fields(output) {
		candidate := strings.TrimPrefix(field, "v")
		if strings.Count(candidate, ".") >= 2 {
			if version, err := Parse(candidate); err == nil {
				return version, nil
			}
		}
	}
	return Version{}, fmt.Errorf("version not found in %q", strings.TrimSpace(output))
}
