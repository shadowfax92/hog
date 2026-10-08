package cmd

import (
	"bytes"
	"strings"
	"testing"

	"hog/internal/proc"
	"hog/internal/reap"
)

func TestReapSummaryShowsProtectionWhenNothingCanBeReaped(t *testing.T) {
	res := reap.Result{
		Scanned: 1, Yours: 1,
		Protected: []reap.Protected{{
			Proc: proc.Proc{PID: 10, FootprintKiB: 256 * 1024},
			Why:  "Apple system executable",
		}},
	}
	var out bytes.Buffer
	printReapSummary(&out, res, reap.Criteria{})
	for _, want := range []string{"Nothing to reap.", "protected: 1 process(es)", "1 × Apple system executable"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary must contain %q, got %q", want, out.String())
		}
	}
}
