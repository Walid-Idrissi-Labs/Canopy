// Package core is the shared contract. It holds the domain types, the state vocabulary, the
// truth and roll-up rules, and the interfaces every other package is written against.
//
// This package is jointly owned. Changing it needs a joint design discussion rather than a
// unilateral commit, because every other package depends on its shape and both maintainers build
// against it at the same time.
//
// Two rules govern what belongs here:
//
//   - The state vocabulary is closed. The test and service states are exactly the ones listed in
//     the corrections document. Adding or removing one is a contract change.
//   - Nothing here imports another internal package. Dependencies point inward only.
//
// Filled in by P1-01 through P1-04.
package core
