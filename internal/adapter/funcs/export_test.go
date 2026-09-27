package funcs

import "time"

// SetDrainTimeoutForTest shortens Close's drain wait; call the returned func to restore it.
func SetDrainTimeoutForTest(d time.Duration) func() {
	orig := drainTimeout
	drainTimeout = d
	return func() { drainTimeout = orig }
}
