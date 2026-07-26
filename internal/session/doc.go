// Package session persists conversations.
//
// SQLite, because sessions, the tool call audit trail, cost history and run reports are all
// queries over the same data, so one storage decision serves four features. Schema migrations from
// the first version, since a tool that loses your history on upgrade is not one anyone keeps.
//
// Compaction shortens what gets sent to a model. It never deletes history, which stays complete
// here and stays searchable.
//
// Filled in by A3-02.
package session
