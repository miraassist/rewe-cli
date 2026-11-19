package main

import (
	"slices"
	"testing"
)

func TestParseArgs(t *testing.T) {
	a, err := parseArgs([]string{"search", "--organic", "milch", "-p", "--category", "a", "--category=b", "--page=2"}, allBoolFlags, allValueFlags)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.pos, []string{"search", "milch"}) || !a.bools["organic"] || !a.bools["pretty"] {
		t.Fatalf("unexpected parse: %+v", a)
	}
	if !slices.Equal(a.values["category"], []string{"a", "b"}) || a.value("page", "") != "2" {
		t.Fatalf("unexpected values: %+v", a.values)
	}

	a, err = parseArgs([]string{"search", "--", "--organic"}, allBoolFlags, allValueFlags)
	if err != nil || !slices.Equal(a.pos, []string{"search", "--organic"}) {
		t.Fatalf("-- must end flag parsing: %+v %v", a, err)
	}
	// "*" and negative-looking values are positionals
	a, _ = parseArgs([]string{"search", "*"}, allBoolFlags, allValueFlags)
	if !slices.Equal(a.pos, []string{"search", "*"}) {
		t.Fatalf("got %+v", a.pos)
	}
	for _, bad := range [][]string{{"--qty"}, {"--bogus"}, {"--organic=yes"}} {
		if _, err := parseArgs(bad, allBoolFlags, allValueFlags); err == nil {
			t.Errorf("%v: expected error", bad)
		}
	}
}

func TestIsUUID(t *testing.T) {
	if !isUUID("0f8fad5b-d9cb-469f-a165-70867728950e") || isUUID("0f8fad5b-d9cb-469f-a165-70867728950") || isUUID("0f8fad5bxd9cb-469f-a165-70867728950e") {
		t.Fatal("isUUID wrong")
	}
}
