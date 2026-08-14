// Command dbosctl is the command-line client for the DBOS Conductor API.
//
// The entrypoint lives here, under cmd/dbosctl/, so the compiled binary is
// named `dbosctl` regardless of the module path; all command logic lives in
// the importable, testable internal/cli package. The `ctl` suffix keeps the
// binary off the name the language SDKs already claim (the DBOS Python SDK
// ships its own `dbos` console script).
package main

import "github.com/dbos-inc/dbos-ctl/internal/cli"

func main() {
	cli.Execute()
}
