package cmd

import (
	"io"
	"strings"
	"testing"

	"hog/internal/group"
)

func TestPidsOfUnion(t *testing.T) {
	groups := []group.Group{
		{App: "Google Chrome", PIDs: []int{1, 2, 3}},
		{App: "Google Chrome Beta", PIDs: []int{4}},
	}
	got := pidsOf(groups)
	want := []int{1, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("pidsOf len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pidsOf[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestConfirmGate(t *testing.T) {
	if confirm(strings.NewReader("n\n"), io.Discard, "") {
		t.Error("confirm(\"n\") = true, want false")
	}
	if !confirm(strings.NewReader("y\n"), io.Discard, "") {
		t.Error("confirm(\"y\") = false, want true")
	}
	if !confirm(strings.NewReader("YES\n"), io.Discard, "") {
		t.Error("confirm(\"YES\") = false, want true (case-insensitive)")
	}
	if confirm(strings.NewReader("\n"), io.Discard, "") {
		t.Error("confirm(enter) = true, want false (default No)")
	}
}
