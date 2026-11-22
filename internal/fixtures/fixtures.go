// Package fixtures holds rule texts that tests use as a realistic setup. It is
// imported by tests and by the cmd/subcheck diagnostic, never by the program:
// what a person's rules say is theirs to write, so none of it ships in Faro.
package fixtures

import _ "embed"

//go:embed ai-accounts.rules
var aiAccounts string

// AIAccounts is a setup that holds some accounts to fixed exits, with a gate.
func AIAccounts() string { return aiAccounts }
