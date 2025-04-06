/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package clusterorder

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	fulfillmentv1 "github.com/innabox/fulfillment-cli/internal/api/fulfillment/v1"
	"github.com/innabox/fulfillment-cli/internal/config"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:     "clusterorder [flags]",
		Aliases: []string{"clusterorders"},
		Short:   "Get cluster orders",
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
	conn, err := cfg.Connect()
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	// Create the client for the cluster orders service:
	client := fulfillmentv1.NewClusterOrdersClient(conn)

	// Get the list of orders:
	response, err := client.List(ctx, &fulfillmentv1.ClusterOrdersListRequest{})
	if err != nil {
		return fmt.Errorf("failed to list orders: %w", err)
	}

	// Display the orders:
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(writer, "ID\tTEMPLATE ID\tSTATE\n")
	for _, order := range response.Items {
		templateId := "-"
		if order.Spec != nil {
			templateId = order.Spec.TemplateId
		}
		state := "-"
		if order.Status != nil {
			state = order.Status.State.String()
			state = strings.Replace(state, "CLUSTER_ORDER_STATE_", "", -1)
		}
		fmt.Fprintf(
			writer,
			"%s\t%s\t%s\n",
			order.Id,
			templateId,
			state,
		)
	}
	writer.Flush()

	return nil
}
