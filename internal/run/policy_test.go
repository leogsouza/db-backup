package run

import "testing"

func TestFailurePolicy(t *testing.T) {
	if retentionAllowed(false) {
		t.Fatal("retention ran after failure")
	}
	if !mustDeletePartial(false) {
		t.Fatal("partial object kept")
	}
	if !retentionAllowed(true) || mustDeletePartial(true) {
		t.Fatal("success policy")
	}
}
