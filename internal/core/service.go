package core

import "time"

// ServiceState is the state of a service as shown to the user.
//
// The vocabulary is closed, exactly as for TestState. These eight values come from the truth
// contract and AllServiceStates is asserted against them in the tests.
type ServiceState string

const (
	// ServiceNotConfigured means no service was configured. Not the same as stopped.
	ServiceNotConfigured ServiceState = "not-configured"
	// ServiceStopped means the service is not running and is not expected to be.
	ServiceStopped ServiceState = "stopped"
	// ServiceStarting means a process exists but has not yet passed a readiness probe.
	ServiceStarting ServiceState = "starting"
	// ServiceHealthy means the configured probe succeeded within its timeout.
	ServiceHealthy ServiceState = "healthy"
	// ServiceUnhealthy means the service is up in some sense but failing its probe. A process can
	// be alive with its port open and still be unhealthy, which is the whole reason process state
	// and readiness are tracked separately.
	ServiceUnhealthy ServiceState = "unhealthy"
	// ServiceCrashed means the process exited unexpectedly.
	ServiceCrashed ServiceState = "crashed"
	// ServiceStopping means a shutdown is in progress.
	ServiceStopping ServiceState = "stopping"
	// ServiceUnknown means the service could not be observed. Never green.
	ServiceUnknown ServiceState = "unknown"
)

// AllServiceStates returns every valid service state, in the order the truth contract lists them.
func AllServiceStates() []ServiceState {
	return []ServiceState{
		ServiceNotConfigured,
		ServiceStopped,
		ServiceStarting,
		ServiceHealthy,
		ServiceUnhealthy,
		ServiceCrashed,
		ServiceStopping,
		ServiceUnknown,
	}
}

// Valid reports whether s is part of the closed vocabulary.
func (s ServiceState) Valid() bool {
	for _, known := range AllServiceStates() {
		if s == known {
			return true
		}
	}
	return false
}

// IsGreen reports whether this state may contribute to a green roll-up. Only healthy qualifies.
func (s ServiceState) IsGreen() bool {
	return s == ServiceHealthy
}

func (s ServiceState) String() string { return string(s) }

// ProbeKind is how readiness is determined for a service.
type ProbeKind string

const (
	// ProbeNone means no readiness probe is configured, so only process liveness is known.
	ProbeNone ProbeKind = "none"
	// ProbeProcess checks that a process exists.
	ProbeProcess ProbeKind = "process"
	// ProbeTCP checks that a TCP connection can be established.
	ProbeTCP ProbeKind = "tcp"
	// ProbeHTTP performs an HTTP request and checks the status against the configured range.
	ProbeHTTP ProbeKind = "http"
)

// AllProbeKinds returns every valid probe kind.
func AllProbeKinds() []ProbeKind {
	return []ProbeKind{ProbeNone, ProbeProcess, ProbeTCP, ProbeHTTP}
}

// Valid reports whether k is a known probe kind.
func (k ProbeKind) Valid() bool {
	for _, known := range AllProbeKinds() {
		if k == known {
			return true
		}
	}
	return false
}

func (k ProbeKind) String() string { return string(k) }

// ServiceInstance identifies one concrete running service, so that evidence can be attributed to
// the right process.
//
// This exists because a probe result on its own proves very little. An HTTP 200 on port 4100
// proves something is listening on port 4100, not that it is this workspace's dev server. Without
// recording which process and which start time a result came from, a stale server from a deleted
// worktree, or an unrelated program that grabbed the port, would both read as a healthy service.
// That is one of the cheapest ways to ship a false green.
type ServiceInstance struct {
	WorkspaceID  string
	ServiceName  string
	InstanceID   string
	PID          int
	ProcessGroup int
	Port         int
	StartedAt    time.Time
}

// ServiceHealth is the result of one health observation.
//
// Process liveness and readiness are recorded separately and are both three valued, so that "we
// did not check" is distinguishable from "we checked and it was not".
type ServiceHealth struct {
	WorkspaceID string
	ServiceName string

	// State is the roll-up of the observations below, and is what the dashboard shows.
	State ServiceState

	// ProcessAlive is whether a process was found. Unknown when liveness was not checked.
	ProcessAlive Observation

	// Ready is whether the readiness probe succeeded. Unknown when no probe is configured, which
	// is why a service with no probe can never be reported as healthy on liveness alone.
	Ready Observation

	// Probe is how readiness was determined.
	Probe ProbeKind

	// InstanceID ties this observation to the ServiceInstance it describes. An observation whose
	// instance no longer matches the running service is evidence about something else.
	InstanceID string

	CheckedAt time.Time
	Latency   time.Duration

	// FailureReason explains an unhealthy or unknown state in words a user can act on, for
	// example "connection refused" or "probe timed out after 2s".
	FailureReason string

	// ConsecutiveFailures counts probe failures in a row, so a single blip is distinguishable
	// from a service that is genuinely down.
	ConsecutiveFailures int
}
