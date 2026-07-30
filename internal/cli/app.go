package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/api"
	"github.com/dbos-inc/dbos-cli/internal/output"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Inspect and manage applications",
}

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List applications",
	Args:  cobra.NoArgs,
	RunE:  runAppList,
}

var appRegisterCmd = &cobra.Command{
	Use:   "register <name>",
	Short: "Register an application",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppRegister,
}

var appDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete an application",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppDelete,
}

var appGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show an application's details",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppGet,
}

var appVersionsCmd = &cobra.Command{
	Use:   "versions <name>",
	Short: "List an application's versions",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppVersions,
}

var appExecutorsCmd = &cobra.Command{
	Use:   "executors <name>",
	Short: "List an application's executors",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppExecutors,
}

var appMetricsCmd = &cobra.Command{
	Use:   "metrics <name>",
	Short: "List an application's metrics",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppMetrics,
}

var appUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update an application's settings",
	Args:  cobra.ExactArgs(1),
	RunE:  runAppUpdate,
}

var appSetVersionCmd = &cobra.Command{
	Use:   "set-version <name> <version>",
	Short: "Set an application's latest version",
	Args:  cobra.ExactArgs(2),
	RunE:  runAppSetVersion,
}

func init() {
	// app list is org-scoped (it lists every app in the org), so it honors
	// --org but not --app. The per-app reads name the app positionally.
	addRequestFlags(appListCmd, "profile", "url", "org", "output")
	addRequestFlags(appRegisterCmd, "profile", "url", "org")
	addRequestFlags(appDeleteCmd, "profile", "url", "org")
	appDeleteCmd.Flags().Bool("force", false, "skip the confirmation prompt (required when non-interactive)")
	addRequestFlags(appGetCmd, "profile", "url", "org", "output")
	addRequestFlags(appVersionsCmd, "profile", "url", "org", "output")
	addRequestFlags(appExecutorsCmd, "profile", "url", "org", "output")
	addRequestFlags(appMetricsCmd, "profile", "url", "org", "output")
	appMetricsCmd.Flags().Duration("since", 24*time.Hour, "report the window ending now and starting this long ago")

	// app update patches the self-hosted tuning fields; only the flags you pass
	// are changed.
	addRequestFlags(appUpdateCmd, "profile", "url", "org")
	appUpdateCmd.Flags().Int64("executor-timeout-secs", 0, "seconds before an idle executor is considered gone")
	appUpdateCmd.Flags().Int64("gc-rows-threshold", 0, "workflow rows kept before garbage collection")
	appUpdateCmd.Flags().Int64("gc-time-threshold-ms", 0, "age in ms before a workflow is garbage-collected")
	appUpdateCmd.Flags().Int64("global-timeout-ms", 0, "global workflow timeout in ms")
	appUpdateCmd.Flags().Bool("private-mode", false, "restrict the app to org members")
	addRequestFlags(appSetVersionCmd, "profile", "url", "org")

	appCmd.AddCommand(appListCmd, appRegisterCmd, appDeleteCmd,
		appGetCmd, appVersionsCmd, appExecutorsCmd, appMetricsCmd,
		appUpdateCmd, appSetVersionCmd)
	rootCmd.AddCommand(appCmd)
}

func runAppList(cmd *cobra.Command, _ []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}

	resp, err := c.ListAppsWithResponse(cmd.Context(), org)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}

	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, appColumns())
}

func runAppRegister(cmd *cobra.Command, args []string) error {
	name := args[0]
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	// An empty body is enough to stand up a bare app record (no executor, no
	// deploy); the tuning fields land with D3 `app update`.
	resp, err := c.RegisterAppWithResponse(cmd.Context(), org, name, api.RegisterAppJSONRequestBody{})
	if err != nil {
		return err
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "registered app %q\n", name)
	return nil
}

func runAppDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	force, _ := cmd.Flags().GetBool("force")
	// A destructive delete needs confirmation. Interactively that's the prompt;
	// without a terminal to answer it (a pipe, a file, CI) we refuse rather than
	// delete unattended — --force is required to proceed there.
	if !force {
		if !isInteractive() {
			return fmt.Errorf("refusing to delete app %q without confirmation: re-run with --force (stdin is not a terminal)", name)
		}
		ok, err := confirm(cmd.InOrStdin(), cmd.ErrOrStderr(),
			fmt.Sprintf("Delete app %q? This cannot be undone.", name))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(cmd.ErrOrStderr(), "aborted")
			return nil
		}
	}

	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.DeleteAppWithResponse(cmd.Context(), org, name)
	if err != nil {
		return err
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "deleted app %q\n", name)
	return nil
}

func runAppGet(cmd *cobra.Command, args []string) error {
	name := args[0]
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.GetAppWithResponse(cmd.Context(), org, name)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.Detail(cmd.OutOrStdout(), format, *resp.JSON200, appDetailFields())
}

func runAppVersions(cmd *cobra.Command, args []string) error {
	name := args[0]
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.ListAppVersionsWithResponse(cmd.Context(), org, name)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, appVersionColumns())
}

func runAppExecutors(cmd *cobra.Command, args []string) error {
	name := args[0]
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.ListExecutorsWithResponse(cmd.Context(), org, name)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, appExecutorColumns())
}

func runAppMetrics(cmd *cobra.Command, args []string) error {
	name := args[0]
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	// The metrics endpoint requires an explicit window; default to the last
	// --since (24h) ending now.
	since, _ := cmd.Flags().GetDuration("since")
	end := time.Now()
	params := &api.ListMetricsParams{StartTime: end.Add(-since), EndTime: end}

	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.ListMetricsWithResponse(cmd.Context(), org, name, params)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, appMetricColumns())
}

func runAppUpdate(cmd *cobra.Command, args []string) error {
	name := args[0]
	// Only patch the fields the user actually named.
	var body api.UpdateAppJSONRequestBody
	changed := false
	patchInt64(cmd, "executor-timeout-secs", &body.ExecutorTimeoutSecs, &changed)
	patchInt64(cmd, "gc-rows-threshold", &body.GcRowsThreshold, &changed)
	patchInt64(cmd, "gc-time-threshold-ms", &body.GcTimeThresholdMs, &changed)
	patchInt64(cmd, "global-timeout-ms", &body.GlobalTimeoutMs, &changed)
	if cmd.Flags().Changed("private-mode") {
		v, _ := cmd.Flags().GetBool("private-mode")
		body.PrivateMode = &v
		changed = true
	}
	if !changed {
		return fmt.Errorf("nothing to update: pass at least one field (see `dbos app update --help`)")
	}

	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.UpdateAppWithResponse(cmd.Context(), org, name, body)
	if err != nil {
		return err
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "updated app %q\n", name)
	return nil
}

func runAppSetVersion(cmd *cobra.Command, args []string) error {
	name, version := args[0], args[1]
	c, s, err := clientFor(cmd)
	if err != nil {
		return err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return err
	}
	resp, err := c.SetLatestAppVersionWithResponse(cmd.Context(), org, name,
		api.SetLatestAppVersionJSONRequestBody{VersionName: version})
	if err != nil {
		return err
	}
	if resp.StatusCode() < 200 || resp.StatusCode() >= 300 {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "set latest version of %q to %q\n", name, version)
	return nil
}

// patchInt64 sets *dst to the flag's value (and marks changed) only if the flag
// was passed, so an unset flag leaves the field nil (unchanged in the patch).
func patchInt64(cmd *cobra.Command, flag string, dst **int64, changed *bool) {
	if cmd.Flags().Changed(flag) {
		v, _ := cmd.Flags().GetInt64(flag)
		*dst = &v
		*changed = true
	}
}

// appDetailFields is the label/value projection for `app get`; labels are the
// JSON keys so the table and `-o json` cross-reference cleanly. Absent pointer
// fields render empty and Detail omits them.
func appDetailFields() []output.Field[api.Application] {
	return []output.Field[api.Application]{
		{Label: "name", Value: func(a api.Application) string { return a.Name }},
		{Label: "id", Value: func(a api.Application) string { return a.Id }},
		{Label: "status", Value: func(a api.Application) string { return string(a.Status) }},
		{Label: "language", Value: func(a api.Application) string { return deref(a.Language) }},
		{Label: "orgId", Value: func(a api.Application) string { return a.OrgId }},
		{Label: "dbosCloud", Value: func(a api.Application) string { return strconv.FormatBool(a.DbosCloud) }},
		{Label: "privateMode", Value: func(a api.Application) string { return strconv.FormatBool(a.PrivateMode) }},
		{Label: "executorTimeoutSecs", Value: func(a api.Application) string { return strconv.FormatInt(a.ExecutorTimeoutSecs, 10) }},
		{Label: "gcRowsThreshold", Value: func(a api.Application) string { return derefInt64(a.GcRowsThreshold) }},
		{Label: "gcTimeThresholdMs", Value: func(a api.Application) string { return derefInt64(a.GcTimeThresholdMs) }},
		{Label: "globalTimeoutMs", Value: func(a api.Application) string { return derefInt64(a.GlobalTimeoutMs) }},
	}
}

func appVersionColumns() []output.Column[api.ApplicationVersion] {
	return []output.Column[api.ApplicationVersion]{
		{Header: "NAME", Value: func(v api.ApplicationVersion) string { return v.VersionName }},
		{Header: "ID", Value: func(v api.ApplicationVersion) string { return v.VersionId }},
		{Header: "TIMESTAMP", Value: func(v api.ApplicationVersion) string { return fmtTime(v.VersionTimestamp) }},
		{Header: "CREATED", Value: func(v api.ApplicationVersion) string { return fmtTime(v.CreatedAt) }},
	}
}

func appExecutorColumns() []output.Column[api.Executor] {
	return []output.Column[api.Executor]{
		{Header: "EXECUTOR", Value: func(e api.Executor) string { return e.ExecutorId }},
		{Header: "STATUS", Value: func(e api.Executor) string { return string(e.Status) }},
		{Header: "VERSION", Value: func(e api.Executor) string { return e.AppVersion }},
		{Header: "LANGUAGE", Value: func(e api.Executor) string { return deref(e.Language) }},
		{Header: "HOSTNAME", Value: func(e api.Executor) string { return deref(e.Hostname) }},
		{Header: "UPDATED", Value: func(e api.Executor) string { return fmtTime(e.UpdatedAt) }},
	}
}

func appMetricColumns() []output.Column[api.Metric] {
	return []output.Column[api.Metric]{
		{Header: "METRIC", Value: func(m api.Metric) string { return m.MetricName }},
		{Header: "TYPE", Value: func(m api.Metric) string { return m.MetricType }},
		{Header: "VALUE", Value: func(m api.Metric) string { return strconv.FormatInt(m.Value, 10) }},
		{Header: "BUCKET", Value: func(m api.Metric) string { return fmtTime(m.TimeBucket) }},
		{Header: "GRANULARITY", Value: func(m api.Metric) string { return strconv.FormatInt(int64(m.Granularity), 10) }},
	}
}

// fmtTime renders an API timestamp for a table cell (UTC, RFC3339), or "" for a
// zero time so an absent value is blank rather than year 0001.
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// derefInt64 renders an optional int64 field, empty for nil.
func derefInt64(p *int64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(*p, 10)
}

func appColumns() []output.Column[api.Application] {
	return []output.Column[api.Application]{
		{Header: "NAME", Value: func(a api.Application) string { return a.Name }},
		{Header: "STATUS", Value: func(a api.Application) string { return string(a.Status) }},
		{Header: "LANGUAGE", Value: func(a api.Application) string { return deref(a.Language) }},
	}
}
