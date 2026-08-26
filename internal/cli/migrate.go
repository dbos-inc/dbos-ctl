package cli

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/migrations"
)

// migrate is the only command that opens a database: it creates and upgrades the
// DBOS system schema itself, from migrations vendored into this binary. That is
// deliberate — the schema is shared by every SDK, so provisioning it should not
// require picking one and installing its toolchain.
var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Create or upgrade the DBOS system database",
	Long: `Create or upgrade the DBOS system database.

Applies every migration the system schema is missing, creating the database and
the schema if they do not exist yet. Safe to re-run: migrations already recorded
are skipped, so a database that is up to date is left alone.

This command reaches the database directly, so it takes a database URL rather
than a profile.

Pass --no-listen-notify for a deployment that cannot use LISTEN/NOTIFY, such as
one behind a connection pooler in transaction mode: the triggers that fire
pg_notify are then left out, and the applications fall back to polling.
CockroachDB is detected and needs no flag.`,
	Args: cobra.NoArgs,
	RunE: runMigrate,
}

func init() {
	addMigrateFlags(migrateCmd)
	sysdbCmd.AddCommand(migrateCmd)
}

// addMigrateFlags installs the command's flags. It is separate from init so
// that tests can build a command carrying exactly these definitions rather than
// their own approximation of them.
func addMigrateFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringP("app-role", "r", "", "database role your DBOS application runs as; granted access to the system tables")
	f.String("print-migrations", "", "print the SQL of migrations from `all|N` onward instead of running them")
	f.Bool("print-user-role", false, "print the SQL granting --app-role access to the system tables instead of running it")
	f.Bool("no-listen-notify", false, "omit the triggers that fire pg_notify, for a deployment that cannot use LISTEN/NOTIFY")
	f.Bool("cockroach", false, "render the printed SQL for CockroachDB; live migration detects the server itself")
}

func runMigrate(cmd *cobra.Command, _ []string) error {
	schema, _ := cmd.Flags().GetString("schema")
	appRole, _ := cmd.Flags().GetString("app-role")
	printMigrations, _ := cmd.Flags().GetString("print-migrations")
	printUserRole, _ := cmd.Flags().GetBool("print-user-role")
	printMigrationsSet := cmd.Flags().Changed("print-migrations")
	noListenNotify, _ := cmd.Flags().GetBool("no-listen-notify")
	listenNotify := !noListenNotify
	cockroach, _ := cmd.Flags().GetBool("cockroach")

	if printMigrationsSet || printUserRole {
		// Print modes never connect, and stdout stays pure SQL and comments so
		// it can be redirected straight into a .sql file.
		return printSQL(cmd, schema, appRole, printMigrations, printMigrationsSet, printUserRole, cockroach, listenNotify)
	}
	if cockroach {
		// Nothing to override: a live migration asks the server what it is, and
		// the answer beats anything a flag could say.
		return &exitError{code: 2, msg: "--cockroach only applies to --print-migrations; a live migration detects the server itself"}
	}

	dbURL, err := resolveDBURL(cmd)
	if err != nil {
		return err
	}

	progress := cmd.ErrOrStderr()
	fmt.Fprintf(progress, "Migrating %s (schema %s)\n", maskPassword(dbURL), schema)
	if err := migrations.Apply(cmd.Context(), dbURL, schema, listenNotify, progress); err != nil {
		return err
	}
	if appRole != "" {
		return migrations.Grant(cmd.Context(), dbURL, appRole, schema, progress)
	}
	return nil
}

// printSQL handles --print-migrations and --print-user-role. The two are
// separate scripts run by different people at different times — the migrations
// by whoever owns the database, the grants by whoever owns the role — so asking
// for both at once is a usage error rather than a concatenation.
func printSQL(cmd *cobra.Command, schema, appRole, printMigrations string, printMigrationsSet, printUserRole, cockroach, listenNotify bool) error {
	if printMigrationsSet && printUserRole {
		return &exitError{code: 2, msg: "--print-user-role cannot be combined with --print-migrations"}
	}
	// The same guard Apply and Grant enforce, run early so a print never
	// renders a name it would refuse to migrate, and so a typo is a usage
	// error rather than a failure further in.
	if err := migrations.ValidateSchemaName(schema); err != nil {
		return &exitError{code: 2, msg: err.Error()}
	}
	out := cmd.OutOrStdout()

	if printUserRole {
		if appRole == "" {
			return &exitError{code: 2, msg: "--print-user-role requires --app-role"}
		}
		if err := migrations.ValidateRoleName(appRole); err != nil {
			return &exitError{code: 2, msg: err.Error()}
		}
		fmt.Fprintf(out, "-- Permissions on DBOS schema %s for role %s\n", schema, appRole)
		for _, query := range migrations.GrantQueries(appRole, schema) {
			fmt.Fprintf(out, "%s;\n", query)
		}
		return nil
	}

	from := 1
	if printMigrations != "all" {
		n, err := strconv.Atoi(printMigrations)
		if err != nil {
			return &exitError{code: 2, msg: fmt.Sprintf("invalid --print-migrations value %q: expected 'all' or a migration number", printMigrations)}
		}
		from = n
	}
	// CockroachDB has no LISTEN/NOTIFY on any version, so asking for the dialect
	// settles the other switch rather than conflicting with it.
	if cockroach {
		listenNotify = false
	}
	statements, err := migrations.Statements(schema, from, cockroach, listenNotify)
	if err != nil {
		return err
	}
	// Naming the database is a comment, not a connection: print mode still
	// works with no URL at all, and then simply does not name one.
	dbURL, _ := resolveDBURL(cmd)
	writeMigrationHeader(out, dbURL, from, cockroach, listenNotify)
	for _, stmt := range statements {
		fmt.Fprintln(out, stmt)
	}
	return nil
}

// writeMigrationHeader writes the comment block above a printed script: what it
// is, and the two things that decide whether running it will work.
func writeMigrationHeader(out io.Writer, dbURL string, from int, cockroach, listenNotify bool) {
	header := "-- DBOS system database migrations"
	if dbURL != "" {
		header += " for " + maskPassword(dbURL)
	}
	fmt.Fprintln(out, header)
	// Which of the variants this is has to be legible in the file itself:
	// nothing downstream can tell them apart by reading the SQL, and applying
	// the wrong one leaves a database that does not match what the applications
	// expect — or, for the dialect, one that fails halfway through.
	if cockroach {
		fmt.Fprintln(out, "-- Generated with --cockroach: for CockroachDB, NOT PostgreSQL.")
	} else {
		// CockroachDB's script has no CONCURRENTLY in it, so the warning would
		// send its reader looking for something that is not there.
		fmt.Fprintln(out, "-- Contains CREATE/DROP INDEX CONCURRENTLY: run outside a transaction block (e.g. plain psql, not psql --single-transaction).")
	}
	if !listenNotify && !cockroach {
		fmt.Fprintln(out, "-- Generated with --no-listen-notify: the pg_notify triggers are NOT included.")
	}
	if from == 1 {
		fmt.Fprintln(out, "-- This script is for FRESH databases only.")
	}
}

// resolveDBURL returns the system database URL from the flag, else the
// environment. There is no config-file source: dbosctl's own config is a set of
// Conductor profiles, and a system database is not one of them.
func resolveDBURL(cmd *cobra.Command) (string, error) {
	dbURL, _ := cmd.Flags().GetString("db-url")
	if dbURL == "" {
		dbURL = os.Getenv(dbURLEnv)
	}
	if dbURL == "" {
		return "", fmt.Errorf("no system database URL; pass -D/--db-url or set $%s", dbURLEnv)
	}
	// The SDKs accept a SQLite system database; this command does not vendor
	// that migration set, and a SQLite file is migrated by the application
	// process that opens it anyway.
	if strings.HasPrefix(dbURL, "sqlite") {
		return "", fmt.Errorf("dbosctl migrates PostgreSQL system databases; %q is SQLite, which the application that opens it migrates itself", dbURL)
	}
	return dbURL, nil
}

// libpqPassword matches the password field of a key/value connection string,
// with or without spaces around the "=".
var libpqPassword = regexp.MustCompile(`(?i)password\s*=\s*[^\s]+`)

// maskPassword renders a connection string for display with its password
// replaced. Progress output naming the database is worth having; the password
// in it is not, since terminals get logged and pasted.
func maskPassword(dbURL string) string {
	if u, err := url.Parse(dbURL); err == nil && u.Scheme != "" {
		if u.User == nil {
			return u.String()
		}
		if _, hasPassword := u.User.Password(); !hasPassword {
			return u.String()
		}
		// Built by hand rather than through url.URL.String(): re-encoding the
		// userinfo would turn the mask itself into %2A%2A%2A.
		masked := u.Scheme + "://" + u.User.Username() + ":***@" + u.Host + u.Path
		if u.RawQuery != "" {
			masked += "?" + u.RawQuery
		}
		if u.Fragment != "" {
			masked += "#" + u.Fragment
		}
		return masked
	}
	return libpqPassword.ReplaceAllString(dbURL, "password=***")
}
