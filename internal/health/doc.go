// Package health probes services that the user started.
//
// Canopy does not start services in v0.1, it observes them. Process liveness, an open TCP socket
// and a healthy HTTP response are three different facts and are kept separate, because a process
// can be alive with its port open and the service still unhealthy.
//
// Every probe records when it ran, how long it took, why it failed and how many consecutive
// failures came before it. Probe results are bound to a recorded service identity, meaning
// workspace, instance, PID and process group, port and start time, so a response from an
// unrelated process listening on the same port is never accepted as proof that the configured
// service is up.
//
// Filled in by P3-06 and P3-07.
package health
