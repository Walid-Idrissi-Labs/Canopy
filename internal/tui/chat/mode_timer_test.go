package chat

import "time"

// The settle delay is a real wait in the product and a formality in a test binary.
//
// Shortened here so the tests that drive the timer through its own command run in microseconds
// instead of spending two seconds each proving something they are not about. They still go through
// the real command and the real generation check, which is the part worth testing; the number of
// seconds is a decision about how the key feels and there is nothing to assert about it.
//
// In a file of the package's own rather than passed in as a field, because nothing outside a test
// has any business changing it. Go links a package's internal and external test files into one
// binary, which is what makes this reach the tests in chat_test.
func init() { modeSettleDelay = 5 * time.Millisecond }
