package ratelimit

import "time"

// TTLSecondsForTest exports ttlSeconds for adversarial tests.
func TTLSecondsForTest(rate, burst float64, floor time.Duration) int {
	return ttlSeconds(rate, burst, floor)
}
