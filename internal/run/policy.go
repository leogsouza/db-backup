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
