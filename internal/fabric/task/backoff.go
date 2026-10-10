package taskfabric

import "time"

// overflowGuard is the largest delay this file will return from doubling: 2^62
// nanoseconds is about 146 years, so a real policy never reaches it. Doubling
// again would pass math.MaxInt64 and wrap into a nonsensically small delay, and
// clamping AFTER that overflow would clamp the wrong number — so the loop stops
// here instead.
const overflowGuard = time.Duration(1) << 62

// retryBackoff returns the delay before the attempt that follows the given
// attempt count. Fail increments Attempts before requeueing, so attempts == 1 is
// the first retry and yields base (1s, 2s, 4s … for base = 1s).
//
// base <= 0 disables backoff and returns 0, which is what keeps the zero value
// "retry immediately" and therefore keeps unconfigured tasks on the 0.3.2
// behaviour. max <= 0 means no cap.
func retryBackoff(attempts int, base, max time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	if attempts < 1 {
		attempts = 1
	}
	if max > 0 && base >= max {
		return max
	}
	delay := base
	for i := 1; i < attempts; i++ {
		if delay >= overflowGuard {
			break
		}
		delay *= 2
		if max > 0 && delay >= max {
			return max
		}
	}
	if max > 0 && delay > max {
		return max
	}
	return delay
}

// nextAttemptAt returns the instant the retry after `attempts` failures may run,
// or the zero time when the policy asks for no delay. Centralised so Fail and
// its callers cannot disagree about "is this requeue immediately runnable?".
func nextAttemptAt(now time.Time, attempts int, base, max time.Duration) time.Time {
	delay := retryBackoff(attempts, base, max)
	if delay <= 0 {
		return time.Time{}
	}
	return now.Add(delay)
}

// backoffMillis converts a policy duration into the whole milliseconds stored in
// event payloads. A positive sub-millisecond duration rounds up to 1ms rather
// than down to 0, because 0 would silently disable the policy after a restore.
func backoffMillis(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	if ms := int(d / time.Millisecond); ms > 0 {
		return ms
	}
	return 1
}
