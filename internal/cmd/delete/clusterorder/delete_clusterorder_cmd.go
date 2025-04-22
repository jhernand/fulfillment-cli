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

	"github.com/spf13/cobra"

	fulfillmentv1 "github.com/innabox/fulfillment-cli/internal/api/fulfillment/v1"
	"github.com/innabox/fulfillment-cli/internal/config"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:     "clusterorder [flags] ID",
		Aliases: []string{"clusterorders"},
		Short:   "Delete a cluster order",
		RunE:    runner.run,
	}
	return result
}

type runnerContext struct {
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	// Check that there is exactly one cluster order ID specified
	if len(args) != 1 {
		fmt.Fprintf(
			os.Stderr,
			"Expected exactly one cluster order ID\n",
		)
		os.Exit(1)
	}
	orderId := args[0]

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

	// Create the client for the cluster orders service:
	client := fulfillmentv1.NewClusterOrdersClient(conn)

	// Get the order:
	_, err = client.Get(ctx, &fulfillmentv1.ClusterOrdersGetRequest{
		Id: orderId,
	})
	if err != nil {
		return fmt.Errorf("failed to retrieve order: %w", err)
	}

	// Delete the order:
	_, err = client.Delete(ctx, &fulfillmentv1.ClusterOrdersDeleteRequest{
		Id: orderId,
	})
	if err != nil {
		return fmt.Errorf("failed to delete order: %w", err)
	}
	fmt.Printf("Deleted cluster order '%s''\n", orderId)

	return nil
}
