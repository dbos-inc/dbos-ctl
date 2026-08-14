package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/dbos-inc/dbos-ctl/internal/api"
	"github.com/dbos-inc/dbos-ctl/internal/output"
)

// Queues are queue *definitions* (GET .../queues). Enqueued workflows are
// `workflow list --queued`, not a subcommand here — see AGENTS.md.
var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Inspect workflow queues",
}

var queueListCmd = &cobra.Command{
	Use:   "list",
	Short: "List queue definitions",
	Args:  cobra.NoArgs,
	RunE:  runQueueList,
}

var queueGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Show a queue's details",
	Args:  cobra.ExactArgs(1),
	RunE:  runQueueGet,
}

func init() {
	addRequestFlags(queueListCmd, "profile", "url", "org", "app", "output")
	addRequestFlags(queueGetCmd, "profile", "url", "org", "app", "output")
	queueCmd.AddCommand(queueListCmd, queueGetCmd)
	rootCmd.AddCommand(queueCmd)
}

func runQueueList(cmd *cobra.Command, _ []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, org, app, err := appScopedTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.ListQueuesWithResponse(cmd.Context(), org, app)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.List(cmd.OutOrStdout(), format, *resp.JSON200, queueColumns())
}

func runQueueGet(cmd *cobra.Command, args []string) error {
	format, err := resolvedFormat(cmd)
	if err != nil {
		return err
	}
	c, org, app, err := appScopedTarget(cmd)
	if err != nil {
		return err
	}
	resp, err := c.GetQueueWithResponse(cmd.Context(), org, app, args[0])
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return apiError(resp.StatusCode(), resp.HTTPResponse.Header, resp.ApplicationproblemJSONDefault, resp.Body)
	}
	return output.Detail(cmd.OutOrStdout(), format, *resp.JSON200, queueFields())
}

func queueColumns() []output.Column[api.Queue] {
	return []output.Column[api.Queue]{
		{Header: "NAME", Value: func(q api.Queue) string { return q.Name }},
		{Header: "CONCURRENCY", Value: func(q api.Queue) string { return derefInt32(q.Concurrency) }},
		{Header: "RATE-LIMIT", Value: func(q api.Queue) string { return derefInt32(q.RateLimitMax) }},
		{Header: "POLLING", Value: func(q api.Queue) string { return fmtFloat(q.PollingIntervalSecs) }},
		{Header: "PRIORITY", Value: func(q api.Queue) string { return strconv.FormatBool(q.PriorityEnabled) }},
	}
}

func queueFields() []output.Field[api.Queue] {
	return []output.Field[api.Queue]{
		{Label: "name", Value: func(q api.Queue) string { return q.Name }},
		{Label: "concurrency", Value: func(q api.Queue) string { return derefInt32(q.Concurrency) }},
		{Label: "workerConcurrency", Value: func(q api.Queue) string { return derefInt32(q.WorkerConcurrency) }},
		{Label: "pollingIntervalSecs", Value: func(q api.Queue) string { return fmtFloat(q.PollingIntervalSecs) }},
		{Label: "priorityEnabled", Value: func(q api.Queue) string { return strconv.FormatBool(q.PriorityEnabled) }},
		{Label: "partitionQueue", Value: func(q api.Queue) string { return strconv.FormatBool(q.PartitionQueue) }},
		{Label: "rateLimitMax", Value: func(q api.Queue) string { return derefInt32(q.RateLimitMax) }},
		{Label: "rateLimitPeriodSecs", Value: func(q api.Queue) string { return derefFloat(q.RateLimitPeriodSecs) }},
	}
}
