package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-cli/internal/api"
	"github.com/dbos-inc/dbos-cli/internal/output"
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

func init() {
	addRequestFlags(scheduleListCmd, "profile", "url", "org", "app", "output")
	addRequestFlags(scheduleGetCmd, "profile", "url", "org", "app", "output")
	scheduleCmd.AddCommand(scheduleListCmd, scheduleGetCmd)
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
		{Label: "status", Value: func(s api.Schedule) string { return s.Status }},
		{Label: "cronExpression", Value: func(s api.Schedule) string { return s.CronExpression }},
		{Label: "cronTimezone", Value: func(s api.Schedule) string { return deref(s.CronTimezone) }},
		{Label: "workflowName", Value: func(s api.Schedule) string { return s.WorkflowName }},
		{Label: "workflowClass", Value: func(s api.Schedule) string { return deref(s.WorkflowClass) }},
		{Label: "automaticBackfill", Value: func(s api.Schedule) string { return strconv.FormatBool(s.AutomaticBackfill) }},
		{Label: "lastFiredAt", Value: func(s api.Schedule) string { return fmtTimePtr(s.LastFiredAt) }},
	}
}
