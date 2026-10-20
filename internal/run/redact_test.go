package run

import "testing"

func TestRedactReplacesPassword(t *testing.T) {
	got := Redact("access denied for s3cret and s3cret", "s3cret")
	if got != "access denied for *** and ***" {
		t.Fatal(got)
	}
}

func TestRedactIgnoresEmptySecret(t *testing.T) {
	got := Redact("no secret here", "")
	if got != "no secret here" {
		t.Fatal(got)
	}
}
