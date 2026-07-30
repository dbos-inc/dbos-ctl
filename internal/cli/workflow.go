package cli

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/api"
	"github.com/dbos-inc/dbos-cli/internal/output"
)

var workflowCmd = &cobra.Command{
	Use:     "workflow",
	Aliases: []string{"wf"},
	Short:   "Inspect and manage workflows",
}

var workflowListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workflows (search-backed, filterable)",
	Long: `List workflows, filtered by the flags below. Without --limit this returns
all matching workflows (so 'workflow list -o ids | workflow cancel -' acts on the
whole set); pass --limit/--offset to bound or page.`,
	Args: cobra.NoArgs,
	RunE: runWorkflowList,
}

var workflowGetCmd = &cobra.Command{
	Use:   "get <workflow-id>",
	Short: "Show a workflow's details",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowGet,
}

var workflowStepsCmd = &cobra.Command{
	Use:   "steps <workflow-id>",
	Short: "List a workflow's steps",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowSteps,
}

var workflowEventsCmd = &cobra.Command{
	Use:   "events <workflow-id>",
	Short: "List a workflow's events",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowEvents,
}

var workflowCancelCmd = &cobra.Command{
	Use:   "cancel <workflow-id>...",
	Short: "Cancel one or more workflows",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runWorkflowCancel,
}

var workflowResumeCmd = &cobra.Command{
	Use:   "resume <workflow-id>...",
	Short: "Resume one or more workflows",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runWorkflowResume,
}

var workflowRestartCmd = &cobra.Command{
	Use:   "restart <workflow-id>...",
	Short: "Restart one or more workflows",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runWorkflowRestart,
}

var workflowDeleteCmd = &cobra.Command{
	Use:   "delete <workflow-id>...",
	Short: "Delete one or more workflows",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runWorkflowDelete,
}

var workflowForkCmd = &cobra.Command{
	Use:   "fork <workflow-id>",
	Short: "Fork a workflow into a new execution",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowFork,
}

func init() {
	// Workflows are app-scoped, so these honor --app (and --org) in addition to
	// the usual request flags.
	for _, c := range []*cobra.Command{workflowListCmd, workflowGetCmd, workflowStepsCmd, workflowEventsCmd} {
		addRequestFlags(c, "profile", "url", "org", "app", "output")
	}
	// `workflow list` filters map to the WorkflowSearchBody. Shorthands avoid the
	// global namespace: no -o on --offset (that's --output), no -v on --app-version
	// (reserved for --verbose). --since/--until take RFC3339 or a duration ("1h").
	lf := workflowListCmd.Flags()
	lf.Int64P("limit", "l", 0, "maximum results")
	lf.Int64("offset", 0, "results to skip")
	lf.StringSlice("id", nil, "filter by workflow ID (repeatable)")
	lf.StringSliceP("user", "u", nil, "filter by user (repeatable)")
	lf.StringSliceP("status", "s", nil, "filter by status (repeatable)")
	lf.StringSliceP("name", "n", nil, "filter by workflow name (repeatable)")
	lf.StringSlice("app-version", nil, "filter by application version (repeatable)")
	lf.StringSlice("queue", nil, "filter by queue name (repeatable)")
	lf.String("since", "", "window start (RFC3339 or a duration like 1h)")
	lf.String("until", "", "window end (RFC3339 or a duration like 1h)")
	lf.Bool("desc", false, "sort newest first")
	lf.Bool("queued", false, "only workflows currently on a queue")

	// Mutations are app-scoped too; they take workflow IDs positionally (a
	// literal `-` reads them from stdin) and support -o ids.
	for _, c := range []*cobra.Command{workflowCancelCmd, workflowResumeCmd, workflowRestartCmd, workflowDeleteCmd} {
		addRequestFlags(c, "profile", "url", "org", "app", "output")
	}
	workflowCancelCmd.Flags().Bool("children", false, "also cancel child workflows")
	workflowDeleteCmd.Flags().Bool("children", false, "also delete child workflows")
	workflowResumeCmd.Flags().String("queue", "", "resume onto this queue")

	// fork is single-workflow and returns the new workflow ID (scalar output).
	addRequestFlags(workflowForkCmd, "profile", "url", "org", "app", "output")
	workflowForkCmd.Flags().String("new-id", "", "ID for the forked workflow (default: generated)")
	workflowForkCmd.Flags().Int32("start-step", 0, "step to fork from")
	workflowForkCmd.Flags().String("queue", "", "enqueue the fork onto this queue")
	workflowForkCmd.Flags().String("app-version", "", "application version for the fork")

	workflowCmd.AddCommand(workflowListCmd, workflowGetCmd, workflowStepsCmd, workflowEventsCmd,
		workflowCancelCmd, workflowResumeCmd, workflowRestartCmd, workflowDeleteCmd, workflowForkCmd)
	rootCmd.AddCommand(workflowCmd)
}

func runWorkflowList(cmd *cobra.Command, _ []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	body, err := workflowSearchBody(cmd)
	if err != nil {
		return err
	}
	c, org, app, err := workflowTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.SearchWorkflowsWithResponse(cmd.Context(), org, app, body)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	if format == output.FormatIDs {
		return output.WriteIDs(cmd.OutOrStdout(), workflowIDs(*resp.JSON200))
	}
	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, workflowListColumns())
}

// workflowSearchBody builds the search request from the filter flags; only the
// flags actually passed are included, so an omitted filter isn't sent.
func workflowSearchBody(cmd *cobra.Command) (api.SearchWorkflowsJSONRequestBody, error) {
	var b api.SearchWorkflowsJSONRequestBody
	searchSlice(cmd, "id", &b.WorkflowIds)
	searchSlice(cmd, "user", &b.User)
	searchSlice(cmd, "status", &b.Status)
	searchSlice(cmd, "name", &b.WorkflowName)
	searchSlice(cmd, "app-version", &b.AppVersion)
	searchSlice(cmd, "queue", &b.QueueName)
	if cmd.Flags().Changed("limit") {
		v, _ := cmd.Flags().GetInt64("limit")
		b.Limit = &v
	}
	if cmd.Flags().Changed("offset") {
		v, _ := cmd.Flags().GetInt64("offset")
		b.Offset = &v
	}
	if cmd.Flags().Changed("desc") {
		v, _ := cmd.Flags().GetBool("desc")
		b.SortDesc = &v
	}
	if cmd.Flags().Changed("queued") {
		v, _ := cmd.Flags().GetBool("queued")
		b.QueuesOnly = &v
	}
	if cmd.Flags().Changed("since") {
		t, err := parseSearchTime(cmd, "since")
		if err != nil {
			return b, err
		}
		b.StartTime = &t
	}
	if cmd.Flags().Changed("until") {
		t, err := parseSearchTime(cmd, "until")
		if err != nil {
			return b, err
		}
		b.EndTime = &t
	}
	return b, nil
}

// searchSlice sets *dst to a repeatable flag's values only if it was passed.
func searchSlice(cmd *cobra.Command, flag string, dst **[]string) {
	if cmd.Flags().Changed(flag) {
		v, _ := cmd.Flags().GetStringSlice(flag)
		*dst = &v
	}
}

// parseSearchTime reads a time flag as either a duration ago ("1h" → now-1h) or
// an absolute RFC3339 timestamp.
func parseSearchTime(cmd *cobra.Command, flag string) (time.Time, error) {
	s, _ := cmd.Flags().GetString(flag)
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid --%s %q (want an RFC3339 timestamp or a duration like 1h)", flag, s)
}

func workflowListColumns() []output.Column[api.Workflow] {
	return []output.Column[api.Workflow]{
		{Header: "WORKFLOW-ID", Value: func(w api.Workflow) string { return w.WorkflowId }},
		{Header: "NAME", Value: func(w api.Workflow) string { return deref(w.WorkflowName) }},
		{Header: "STATUS", Value: func(w api.Workflow) string { return w.Status }},
		{Header: "QUEUE", Value: func(w api.Workflow) string { return deref(w.QueueName) }},
		{Header: "CREATED", Value: func(w api.Workflow) string { return fmtTime(w.CreatedAt) }},
	}
}

func workflowIDs(ws []api.Workflow) []string {
	ids := make([]string, len(ws))
	for i, w := range ws {
		ids[i] = w.WorkflowId
	}
	return ids
}

func runWorkflowCancel(cmd *cobra.Command, args []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	ids, err := collectWorkflowIDs(cmd, args)
	if err != nil {
		return err
	}
	c, org, app, err := workflowTarget(cmd)
	if err != nil {
		return err
	}
	children := optBool(cmd, "children")
	if len(ids) == 1 {
		resp, err := c.CancelWorkflowWithResponse(cmd.Context(), org, app, ids[0],
			api.CancelWorkflowJSONRequestBody{CancelChildren: children})
		if err != nil {
			return err
		}
		if err := checkStatus(resp.StatusCode(), resp.HTTPResponse, resp.ApplicationproblemJSONDefault, resp.Body); err != nil {
			return err
		}
	} else {
		resp, err := c.BulkCancelWorkflowsWithResponse(cmd.Context(), org, app,
			api.BulkCancelWorkflowsJSONRequestBody{WorkflowIds: ids, CancelChildren: children})
		if err != nil {
			return err
		}
		if err := checkStatus(resp.StatusCode(), resp.HTTPResponse, resp.ApplicationproblemJSONDefault, resp.Body); err != nil {
			return err
		}
	}
	return reportMutation(cmd, format, "cancelled", ids)
}

func runWorkflowResume(cmd *cobra.Command, args []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	ids, err := collectWorkflowIDs(cmd, args)
	if err != nil {
		return err
	}
	c, org, app, err := workflowTarget(cmd)
	if err != nil {
		return err
	}
	queue := optString(cmd, "queue")
	if len(ids) == 1 {
		resp, err := c.ResumeWorkflowWithResponse(cmd.Context(), org, app, ids[0],
			api.ResumeWorkflowJSONRequestBody{QueueName: queue})
		if err != nil {
			return err
		}
		if err := checkStatus(resp.StatusCode(), resp.HTTPResponse, resp.ApplicationproblemJSONDefault, resp.Body); err != nil {
			return err
		}
	} else {
		resp, err := c.BulkResumeWorkflowsWithResponse(cmd.Context(), org, app,
			api.BulkResumeWorkflowsJSONRequestBody{WorkflowIds: ids, QueueName: queue})
		if err != nil {
			return err
		}
		if err := checkStatus(resp.StatusCode(), resp.HTTPResponse, resp.ApplicationproblemJSONDefault, resp.Body); err != nil {
			return err
		}
	}
	return reportMutation(cmd, format, "resumed", ids)
}

// runWorkflowRestart loops single restarts — there is no bulk-restart endpoint.
func runWorkflowRestart(cmd *cobra.Command, args []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	ids, err := collectWorkflowIDs(cmd, args)
	if err != nil {
		return err
	}
	c, org, app, err := workflowTarget(cmd)
	if err != nil {
		return err
	}
	for _, id := range ids {
		resp, err := c.RestartWorkflowWithResponse(cmd.Context(), org, app, id)
		if err != nil {
			return err
		}
		if err := checkStatus(resp.StatusCode(), resp.HTTPResponse, resp.ApplicationproblemJSONDefault, resp.Body); err != nil {
			return err
		}
	}
	return reportMutation(cmd, format, "restarted", ids)
}

func runWorkflowDelete(cmd *cobra.Command, args []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	ids, err := collectWorkflowIDs(cmd, args)
	if err != nil {
		return err
	}
	c, org, app, err := workflowTarget(cmd)
	if err != nil {
		return err
	}
	children := optBool(cmd, "children")
	if len(ids) == 1 {
		resp, err := c.DeleteWorkflowWithResponse(cmd.Context(), org, app, ids[0],
			&api.DeleteWorkflowParams{DeleteChildren: children})
		if err != nil {
			return err
		}
		if err := checkStatus(resp.StatusCode(), resp.HTTPResponse, resp.ApplicationproblemJSONDefault, resp.Body); err != nil {
			return err
		}
	} else {
		resp, err := c.BulkDeleteWorkflowsWithResponse(cmd.Context(), org, app,
			api.BulkDeleteWorkflowsJSONRequestBody{WorkflowIds: ids, DeleteChildren: children})
		if err != nil {
			return err
		}
		if err := checkStatus(resp.StatusCode(), resp.HTTPResponse, resp.ApplicationproblemJSONDefault, resp.Body); err != nil {
			return err
		}
	}
	return reportMutation(cmd, format, "deleted", ids)
}

// runWorkflowFork forks one workflow and prints the new workflow ID (scalar
// convention — bare on stdout, so `$(dbos workflow fork x)` is usable), or the
// raw ForkWorkflowOutputBody under -o json.
func runWorkflowFork(cmd *cobra.Command, args []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	body := api.ForkWorkflowJSONRequestBody{
		NewWorkflowId: optString(cmd, "new-id"),
		QueueName:     optString(cmd, "queue"),
		AppVersion:    optString(cmd, "app-version"),
	}
	if cmd.Flags().Changed("start-step") {
		v, _ := cmd.Flags().GetInt32("start-step")
		body.StartStep = &v
	}
	c, org, app, err := workflowTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.ForkWorkflowWithResponse(cmd.Context(), org, app, args[0], body)
	if err != nil {
		return err
	}
	if resp.JSON201 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	if format == output.FormatJSON {
		return output.JSON(cmd.OutOrStdout(), resp.JSON201)
	}
	fmt.Fprintln(cmd.OutOrStdout(), resp.JSON201.WorkflowId)
	return nil
}

// collectWorkflowIDs returns the workflow IDs from args, expanding a literal "-"
// to IDs read from stdin (one per line, blanks skipped) so
// `dbos workflow list -o ids | dbos workflow cancel -` works.
func collectWorkflowIDs(cmd *cobra.Command, args []string) ([]string, error) {
	var ids []string
	for _, a := range args {
		if a != "-" {
			ids = append(ids, a)
			continue
		}
		sc := bufio.NewScanner(cmd.InOrStdin())
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" {
				ids = append(ids, line)
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no workflow IDs given")
	}
	return ids, nil
}

// reportMutation prints the affected IDs (one per line) for -o ids, else a human
// confirmation.
func reportMutation(cmd *cobra.Command, format output.Format, verb string, ids []string) error {
	if format == output.FormatIDs {
		return output.WriteIDs(cmd.OutOrStdout(), ids)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %d workflow(s)\n", verb, len(ids))
	return nil
}

// optBool / optString return a flag's value as a pointer only if it was passed,
// so an unset flag is omitted from the request body.
func optBool(cmd *cobra.Command, flag string) *bool {
	if cmd.Flags().Changed(flag) {
		v, _ := cmd.Flags().GetBool(flag)
		return &v
	}
	return nil
}

func optString(cmd *cobra.Command, flag string) *string {
	if cmd.Flags().Changed(flag) {
		v, _ := cmd.Flags().GetString(flag)
		return &v
	}
	return nil
}

// workflowTarget resolves the client plus the org and app an app-scoped workflow
// request needs.
func workflowTarget(cmd *cobra.Command) (*api.ClientWithResponses, string, string, error) {
	c, s, err := clientFor(cmd)
	if err != nil {
		return nil, "", "", err
	}
	org, err := effectiveOrg(cmd.Context(), c, s)
	if err != nil {
		return nil, "", "", err
	}
	app, err := effectiveApp(s)
	if err != nil {
		return nil, "", "", err
	}
	return c, org, app, nil
}

func runWorkflowGet(cmd *cobra.Command, args []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, org, app, err := workflowTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.GetWorkflowWithResponse(cmd.Context(), org, app, args[0])
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.Detail(cmd.OutOrStdout(), format, *resp.JSON200, workflowDetailFields())
}

func runWorkflowSteps(cmd *cobra.Command, args []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, org, app, err := workflowTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.ListWorkflowStepsWithResponse(cmd.Context(), org, app, args[0], nil)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, stepColumns())
}

func runWorkflowEvents(cmd *cobra.Command, args []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, org, app, err := workflowTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.ListWorkflowEventsWithResponse(cmd.Context(), org, app, args[0])
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, eventColumns())
}

// workflowDetailFields is the label/value projection for `workflow get`; labels
// are the JSON keys and empty/absent fields are omitted by Detail.
func workflowDetailFields() []output.Field[api.Workflow] {
	str := func(f func(api.Workflow) *string) func(api.Workflow) string {
		return func(w api.Workflow) string { return deref(f(w)) }
	}
	return []output.Field[api.Workflow]{
		{Label: "workflowId", Value: func(w api.Workflow) string { return w.WorkflowId }},
		{Label: "workflowName", Value: str(func(w api.Workflow) *string { return w.WorkflowName })},
		{Label: "status", Value: func(w api.Workflow) string { return w.Status }},
		{Label: "appVersion", Value: str(func(w api.Workflow) *string { return w.AppVersion })},
		{Label: "executorId", Value: str(func(w api.Workflow) *string { return w.ExecutorId })},
		{Label: "queueName", Value: str(func(w api.Workflow) *string { return w.QueueName })},
		{Label: "scheduleName", Value: str(func(w api.Workflow) *string { return w.ScheduleName })},
		{Label: "priority", Value: func(w api.Workflow) string { return strconv.FormatInt(int64(w.Priority), 10) }},
		{Label: "createdAt", Value: func(w api.Workflow) string { return fmtTime(w.CreatedAt) }},
		{Label: "updatedAt", Value: func(w api.Workflow) string { return fmtTime(w.UpdatedAt) }},
		{Label: "completedAt", Value: func(w api.Workflow) string { return fmtTimePtr(w.CompletedAt) }},
		{Label: "deadline", Value: func(w api.Workflow) string { return fmtTimePtr(w.Deadline) }},
		{Label: "timeoutMs", Value: func(w api.Workflow) string { return derefInt64(w.TimeoutMs) }},
		{Label: "user", Value: str(func(w api.Workflow) *string { return w.User })},
		{Label: "workflowClass", Value: str(func(w api.Workflow) *string { return w.WorkflowClass })},
		{Label: "parentWorkflowId", Value: str(func(w api.Workflow) *string { return w.ParentWorkflowId })},
		{Label: "forkedFrom", Value: str(func(w api.Workflow) *string { return w.ForkedFrom })},
		{Label: "deduplicationId", Value: str(func(w api.Workflow) *string { return w.DeduplicationId })},
		{Label: "input", Value: str(func(w api.Workflow) *string { return w.Input })},
		{Label: "output", Value: str(func(w api.Workflow) *string { return w.Output })},
		{Label: "error", Value: str(func(w api.Workflow) *string { return w.Error })},
	}
}

func stepColumns() []output.Column[api.Step] {
	return []output.Column[api.Step]{
		{Header: "STEP", Value: func(s api.Step) string { return strconv.FormatInt(int64(s.StepId), 10) }},
		{Header: "NAME", Value: func(s api.Step) string { return s.StepName }},
		{Header: "STARTED", Value: func(s api.Step) string { return fmtTimePtr(s.StartedAt) }},
		{Header: "COMPLETED", Value: func(s api.Step) string { return fmtTimePtr(s.CompletedAt) }},
		{Header: "CHILD", Value: func(s api.Step) string { return deref(s.ChildWorkflowId) }},
		{Header: "ERROR", Value: func(s api.Step) string { return deref(s.Error) }},
	}
}

func eventColumns() []output.Column[api.Event] {
	return []output.Column[api.Event]{
		{Header: "KEY", Value: func(e api.Event) string { return e.Key }},
		{Header: "VALUE", Value: func(e api.Event) string { return e.Value }},
	}
}
