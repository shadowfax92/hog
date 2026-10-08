package reap

import "testing"

func TestLaunchdRegistryIncludesOnlyRunningJobs(t *testing.T) {
	jobs, err := parseLaunchdJobs("PID\tStatus\tLabel\n42\t0\texample.agent\n-\t0\texample.idle\n43\t-9\texample.restarted\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || !jobs[42] || !jobs[43] {
		t.Fatalf("want both live jobs, including a restarted job, got %v", jobs)
	}
}

func TestLaunchdRegistryRejectsUnavailableOrMalformedOutput(t *testing.T) {
	for _, raw := range []string{
		"", "permission denied", "PID Status Label\n42", "PID Status Label\n42 unknown example.agent",
		"PID Status Label\nnot-a-pid 0 example.agent", "PID Status Label\n0 0 example.agent",
	} {
		if _, err := parseLaunchdJobs(raw); err == nil {
			t.Errorf("unusable registry must fail closed, accepted %q", raw)
		}
	}
}
