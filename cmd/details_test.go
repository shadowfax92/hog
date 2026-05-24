package cmd

import "testing"

func TestParsePickedPIDs(t *testing.T) {
	// fzf echoes back the selected lines verbatim; the PID is the first column.
	out := "84956   103%   516M  node /Users/x/.cc2cc/server.mjs\n" +
		"89811   102%   382M  node /Users/x/.cc2cc/server.mjs\n" +
		"\n" +
		"   \n"
	got := parsePickedPIDs(out)
	if len(got) != 2 || got[0] != 84956 || got[1] != 89811 {
		t.Fatalf("parsePickedPIDs = %v, want [84956 89811]", got)
	}
}
