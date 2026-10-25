package run

// retentionAllowed reports whether retention may run.
// It is true only when the pipeline succeeded.
func retentionAllowed(pipelineOK bool) bool {
	return pipelineOK
}

// mustDeletePartial reports whether this run's remote object must be deleted.
// It is true when the pipeline did not succeed.
func mustDeletePartial(pipelineOK bool) bool {
	return !pipelineOK
}

// uploadAllowed reports whether the dump may be copied to the bucket.
// It is true only when dump and gzip succeeded and the run was not canceled.
func uploadAllowed(dumpOK, gzipOK, canceled bool) bool {
	return dumpOK && gzipOK && !canceled
}

// uploadSucceeded reports whether the object write finished cleanly.
// A copy error, a close error, or cancellation is not success.
func uploadSucceeded(copyErr, closeErr error, canceled bool) bool {
	return copyErr == nil && closeErr == nil && !canceled
}

// shouldDeleteObject reports whether this run must delete the remote object.
// It is true only when a writer was opened and the upload did not succeed.
func shouldDeleteObject(writerOpened, uploadOK bool) bool {
	return writerOpened && !uploadOK
}

// retainOutcome returns ctxErr when the context failed.
// A retention operation error does not fail the backup.
func retainOutcome(ctxErr, opErr error) error {
	if ctxErr != nil {
		return ctxErr
	}
	return nil
}

// retentionListLog formats a redacted retention list failure.
func retentionListLog(err error, password string) string {
	return Redact("db-backup: retenção: falha ao listar: "+err.Error(), password)
}

// retentionDeleteLog formats a redacted retention delete failure.
// The object URI is gs:// plus bucket, slash, and name.
func retentionDeleteLog(bucket, name string, err error, password string) string {
	uri := "gs://" + bucket + "/" + name
	return Redact("db-backup: retenção: falha ao apagar "+uri+": "+err.Error(), password)
}

// partialDeleteLog formats a redacted partial-object delete failure.
func partialDeleteLog(detail, password string) string {
	return Redact("db-backup: falha ao apagar objeto parcial: "+detail, password)
}
