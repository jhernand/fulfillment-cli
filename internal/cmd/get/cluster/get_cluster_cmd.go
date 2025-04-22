/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cluster

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	fulfillmentv1 "github.com/innabox/fulfillment-cli/internal/api/fulfillment/v1"
	"github.com/innabox/fulfillment-cli/internal/config"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:     "cluster [flags]",
		Aliases: []string{"clusters"},
		Short:   "Get clusters",
		RunE:    runner.run,
	}
	return result
}

type runnerContext struct {
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	// Get the context:
	ctx := cmd.Context()

	// Get the configuration:
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Address == "" {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Create the gRPC connection from the configuration:
	conn, err := cfg.Connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	// Create the client for the clusters service:
	client := fulfillmentv1.NewClustersClient(conn)

	// Get the list of clusters:
	response, err := client.List(ctx, &fulfillmentv1.ClustersListRequest{})
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	// Display the clusters:
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(writer, "ID\tSTATE\tAPI URL\tCONSOLE URL\n")
	for _, cluster := range response.Items {
		state := "-"
		apiUrl := "-"
		consoleUrl := "-"
		if cluster.Status != nil {
			state = cluster.Status.State.String()
			apiUrl = cluster.Status.GetApiUrl()
			consoleUrl = cluster.Status.GetConsoleUrl()
		}
		fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\n",
			cluster.Id,
			state,
			apiUrl,
			consoleUrl,
		)
	}
	writer.Flush()

	return nil
}
