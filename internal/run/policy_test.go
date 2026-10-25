package run

import (
	"context"
	"errors"
	"strings"
	"testing"
)

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

func TestUploadAllowed(t *testing.T) {
	if uploadAllowed(false, true, false) {
		t.Fatal("upload after failed dump")
	}
	if uploadAllowed(true, false, false) {
		t.Fatal("upload after failed gzip")
	}
	if uploadAllowed(true, true, true) {
		t.Fatal("upload after cancel")
	}
	if !uploadAllowed(true, true, false) {
		t.Fatal("upload blocked")
	}
}

func TestUploadSucceeded(t *testing.T) {
	if uploadSucceeded(nil, errors.New("close"), false) {
		t.Fatal("close error treated as success")
	}
	if uploadSucceeded(errors.New("copy"), nil, false) {
		t.Fatal("copy error treated as success")
	}
	if uploadSucceeded(nil, nil, true) {
		t.Fatal("canceled upload treated as success")
	}
	if !uploadSucceeded(nil, nil, false) {
		t.Fatal("clean upload rejected")
	}
}

func TestShouldDeleteObject(t *testing.T) {
	if shouldDeleteObject(false, false) {
		t.Fatal("delete without a writer")
	}
	if !shouldDeleteObject(true, false) {
		t.Fatal("partial object kept")
	}
	if shouldDeleteObject(true, true) {
		t.Fatal("successful object deleted")
	}
}

func TestRetainOutcome(t *testing.T) {
	if retainOutcome(nil, errors.New("list")) != nil {
		t.Fatal("list error failed the backup")
	}
	if retainOutcome(context.Canceled, nil) != context.Canceled {
		t.Fatal("cancel ignored")
	}
}

func TestRetentionDeleteLogRedactsPassword(t *testing.T) {
	got := retentionDeleteLog("backups", "venko-20261019-2315.sql.gz", errors.New("boom s3cret"), "s3cret")
	want := "db-backup: retenção: falha ao apagar gs://backups/venko-20261019-2315.sql.gz: boom ***"
	if got != want {
		t.Fatalf("%q", got)
	}
	if strings.Contains(got, "s3cret") {
		t.Fatal(got)
	}
}

func TestRetentionListAndPartialLogs(t *testing.T) {
	if retentionListLog(errors.New("boom s3cret"), "s3cret") != "db-backup: retenção: falha ao listar: boom ***" {
		t.Fatal(retentionListLog(errors.New("boom s3cret"), "s3cret"))
	}
	if partialDeleteLog("boom s3cret", "s3cret") != "db-backup: falha ao apagar objeto parcial: boom ***" {
		t.Fatal(partialDeleteLog("boom s3cret", "s3cret"))
	}
}
