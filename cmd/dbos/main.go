// Command dbos is the command-line client for the DBOS Conductor API.
//
// The entrypoint lives here, under cmd/dbos/, so the compiled binary is named
// `dbos` regardless of the module path; all command logic lives in the
// importable, testable internal/cli package.
package main

import "github.com/dbos-inc/dbos-cli/internal/cli"

func main() {
	cli.Execute()
}
