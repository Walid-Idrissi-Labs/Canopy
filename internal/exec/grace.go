package exec

import "time"

// gracePeriod is how long a command has to exit after SIGTERM before SIGKILL follows.
//
// Short. The processes that honour SIGTERM do so immediately, and the ones that do not are exactly
// what the second signal is for, so waiting longer only delays the inevitable while a user watches.
const gracePeriod = 250 * time.Millisecond

func afterGracePeriod() <-chan time.Time { return time.After(gracePeriod) }
