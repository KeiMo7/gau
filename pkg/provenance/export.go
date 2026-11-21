package provenance

// AtomicExport renders output to path via a sibling temp file and an atomic
// rename, so a failure during output never leaves a truncated destination.
func AtomicExport(path string, data []byte) error {
	return atomicWrite(path, data)
}
