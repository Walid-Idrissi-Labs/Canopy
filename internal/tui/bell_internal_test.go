package tui

import "io"

// The bell writes to the terminal, which a test cannot listen to.
//
// In a file of the package's own rather than a field on the application, because nothing outside a
// test has any business changing where the bell goes. Go links a package's internal and external
// test files into one binary, which is what lets the tests in tui_test reach this, and it is the
// same arrangement chat uses to shorten the mode settle delay.
func BellHeard(w io.Writer) func() {
	previous := bellOut
	bellOut = w
	return func() { bellOut = previous }
}
