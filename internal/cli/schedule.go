package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/api"
	"github.com/dbos-inc/dbos-ctl/internal/output"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Inspect scheduled workflows",
}

var scheduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List schedules",
	Args:  cobra.NoArgs,
	RunE:  runScheduleList,
}

var scheduleGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show a schedule's details",
	Args:  cobra.ExactArgs(1),
	RunE:  runScheduleGet,
}

var schedulePauseCmd = &cobra.Command{
	Use:   "pause <name>",
	Short: "Pause a schedule",
	Args:  cobra.ExactArgs(1),
	RunE:  runSchedulePause,
}

var scheduleResumeCmd = &cobra.Command{
	Use:   "resume <name>",
	Short: "Resume a paused schedule",
	Args:  cobra.ExactArgs(1),
	RunE:  runScheduleResume,
}

var scheduleTriggerCmd = &cobra.Command{
	Use:   "trigger <name>",
	Short: "Trigger a schedule now (prints the started workflow ID)",
	Args:  cobra.ExactArgs(1),
	RunE:  runScheduleTrigger,
}

var scheduleBackfillCmd = &cobra.Command{
	Use:   "backfill <name>",
	Short: "Backfill a schedule over a time window (prints the started workflow IDs)",
	Args:  cobra.ExactArgs(1),
	RunE:  runScheduleBackfill,
}

func init() {
	addRequestFlags(scheduleListCmd, "profile", "url", "org", "app", "output")
	addRequestFlags(scheduleGetCmd, "profile", "url", "org", "app", "output")
	// pause/resume are plain actions (no structured output); trigger/backfill
	// print scalar workflow IDs and honor -o json for the raw shape.
	addRequestFlags(schedulePauseCmd, "profile", "url", "org", "app")
	addRequestFlags(scheduleResumeCmd, "profile", "url", "org", "app")
	addRequestFlags(scheduleTriggerCmd, "profile", "url", "org", "app", "output")
	addRequestFlags(scheduleBackfillCmd, "profile", "url", "org", "app", "output")
	scheduleBackfillCmd.Flags().String("since", "", "window start (RFC3339 or a duration like 1h)")
	scheduleBackfillCmd.Flags().String("until", "", "window end (RFC3339 or a duration like 1h)")

	scheduleCmd.AddCommand(scheduleListCmd, scheduleGetCmd,
		schedulePauseCmd, scheduleResumeCmd, scheduleTriggerCmd, scheduleBackfillCmd)
	rootCmd.AddCommand(scheduleCmd)
}

func runScheduleList(cmd *cobra.Command, _ []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, org, app, err := appScopedTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.ListSchedulesWithResponse(cmd.Context(), org, app, nil)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, scheduleColumns())
}

func runScheduleGet(cmd *cobra.Command, args []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, org, app, err := appScopedTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.GetScheduleWithResponse(cmd.Context(), org, app, args[0])
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.Detail(cmd.OutOrStdout(), format, *resp.JSON200, scheduleFields())
}

func runSchedulePause(cmd *cobra.Command, args []string) error {
	c, org, app, err := appScopedTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.PauseScheduleWithResponse(cmd.Context(), org, app, args[0])
	if err != nil {
		return err
	}
	if err := checkStatus(resp.StatusCode(), resp.HTTPResponse, resp.ApplicationproblemJSONDefault, resp.Body); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "paused schedule %q\n", args[0])
	return nil
}

func runScheduleResume(cmd *cobra.Command, args []string) error {
	c, org, app, err := appScopedTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.ResumeScheduleWithResponse(cmd.Context(), org, app, args[0])
	if err != nil {
		return err
	}
	if err := checkStatus(resp.StatusCode(), resp.HTTPResponse, resp.ApplicationproblemJSONDefault, resp.Body); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "resumed schedule %q\n", args[0])
	return nil
}

// runScheduleTrigger fires the schedule now and prints the started workflow ID
// (scalar convention — bare stdout, or -o json for the raw shape).
func runScheduleTrigger(cmd *cobra.Command, args []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, org, app, err := appScopedTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.TriggerScheduleWithResponse(cmd.Context(), org, app, args[0])
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

// runScheduleBackfill replays the schedule over [--since, --until] and prints the
// started workflow IDs (one per line — the scalar-list convention).
func runScheduleBackfill(cmd *cobra.Command, args []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	if !cmd.Flags().Changed("since") || !cmd.Flags().Changed("until") {
		return fmt.Errorf("backfill needs both --since and --until")
	}
	start, err := parseSearchTime(cmd, "since")
	if err != nil {
		return err
	}
	end, err := parseSearchTime(cmd, "until")
	if err != nil {
		return err
	}

	c, org, app, err := appScopedTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.BackfillScheduleWithResponse(cmd.Context(), org, app, args[0],
		api.BackfillScheduleJSONRequestBody{StartTime: start, EndTime: end})
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	if format == output.FormatJSON {
		return output.JSON(cmd.OutOrStdout(), resp.JSON200)
	}
	return output.WriteIDs(cmd.OutOrStdout(), resp.JSON200.WorkflowIds)
}

func scheduleColumns() []output.Column[api.Schedule] {
	return []output.Column[api.Schedule]{
		{Header: "NAME", Value: func(s api.Schedule) string { return s.ScheduleName }},
		{Header: "STATUS", Value: func(s api.Schedule) string { return s.Status }},
		{Header: "CRON", Value: func(s api.Schedule) string { return s.CronExpression }},
		{Header: "WORKFLOW", Value: func(s api.Schedule) string { return s.WorkflowName }},
		{Header: "LAST-FIRED", Value: func(s api.Schedule) string { return fmtTimePtr(s.LastFiredAt) }},
	}
}

func scheduleFields() []output.Field[api.Schedule] {
	return []output.Field[api.Schedule]{
		{Label: "scheduleId", Value: func(s api.Schedule) string { return s.ScheduleId }},
		{Label: "scheduleName", Value: func(s api.Schedule) string { return s.ScheduleName }},
		{Label: "applicationName", Value: func(s api.Schedule) string { return deref(s.ApplicationName) }},
		{Label: "status", Value: func(s api.Schedule) string { return s.Status }},
		{Label: "cronExpression", Value: func(s api.Schedule) string { return s.CronExpression }},
		{Label: "cronTimezone", Value: func(s api.Schedule) string { return deref(s.CronTimezone) }},
		{Label: "workflowName", Value: func(s api.Schedule) string { return s.WorkflowName }},
		{Label: "workflowClass", Value: func(s api.Schedule) string { return deref(s.WorkflowClass) }},
		{Label: "automaticBackfill", Value: func(s api.Schedule) string { return strconv.FormatBool(s.AutomaticBackfill) }},
		{Label: "lastFiredAt", Value: func(s api.Schedule) string { return fmtTimePtr(s.LastFiredAt) }},
	}
}
