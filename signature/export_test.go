package signature

// LimitVerify lowers VerifyAll's bounds for a test and returns what restores them.
func LimitVerify(count int, bytes int64) (restore func()) {
	oc, ob := maxVerifications, maxVerifyBytes
	maxVerifications, maxVerifyBytes = count, bytes
	return func() { maxVerifications, maxVerifyBytes = oc, ob }
}
