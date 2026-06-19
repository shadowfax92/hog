package cmd

import (
	"reflect"
	"testing"

	"hog/internal/group"
)

func TestParsePickedIndexes(t *testing.T) {
	out := "2      72%     118M      8  Google Chrome\n" +
		"bad    row\n" +
		"4      12%      52M      2  Activity Monitor\n" +
		"\n"

	got := parsePickedIndexes(out)
	want := []int{2, 4}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePickedIndexes = %v, want %v", got, want)
	}
}

func TestSelectedGroupsByIndexes(t *testing.T) {
	groups := []group.Group{
		{App: "node"},
		{App: "Google Chrome"},
		{App: "Activity Monitor"},
	}

	got := selectedGroupsByIndexes(groups, []int{2, 99, 2, 0, 3})
	want := []group.Group{
		{App: "Google Chrome"},
		{App: "Activity Monitor"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selectedGroupsByIndexes = %#v, want %#v", got, want)
	}
}
