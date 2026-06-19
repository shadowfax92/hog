package cmd

import "testing"

func TestDetailsAndKillAllowOptionalAppArg(t *testing.T) {
	for _, args := range [][]string{nil, []string{"node"}} {
		if err := detailsCmd.Args(detailsCmd, args); err != nil {
			t.Fatalf("details args %v error = %v, want nil", args, err)
		}
		if err := killCmd.Args(killCmd, args); err != nil {
			t.Fatalf("kill args %v error = %v, want nil", args, err)
		}
	}

	if err := detailsCmd.Args(detailsCmd, []string{"node", "extra"}); err == nil {
		t.Fatal("details accepted two args, want error")
	}
	if err := killCmd.Args(killCmd, []string{"node", "extra"}); err == nil {
		t.Fatal("kill accepted two args, want error")
	}
}

func TestParsePickedPIDs(t *testing.T) {
	out := "84956   103%   516M  node /Users/x/.cc2cc/server.mjs\n" +
		"89811   102%   382M  node /Users/x/.cc2cc/server.mjs\n" +
		"\n" +
		"   \n"
	got := parsePickedPIDs(out)
	if len(got) != 2 || got[0] != 84956 || got[1] != 89811 {
		t.Fatalf("parsePickedPIDs = %v, want [84956 89811]", got)
	}
}
