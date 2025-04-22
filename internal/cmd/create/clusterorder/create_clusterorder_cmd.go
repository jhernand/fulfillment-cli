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
	"text/tabwriter"

	"github.com/spf13/cobra"

	fulfillmentv1 "github.com/innabox/fulfillment-cli/internal/api/fulfillment/v1"
	"github.com/innabox/fulfillment-cli/internal/config"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:   "clusterorder [flags]",
		Short: "Create a cluster order",
		RunE:  runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.templateId,
		"template-id",
		"",
		"Template identifier",
	)
	return result
}

type runnerContext struct {
	templateId string
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

	// Check that we have a template:
	if c.templateId == "" {
		return fmt.Errorf("template-id is required")
	}

	// Create the gRPC connection from the configuration:
	conn, err := cfg.Connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	// Create the client for the cluster orders service:
	client := fulfillmentv1.NewClusterOrdersClient(conn)

	// Prepare the order:
	order := &fulfillmentv1.ClusterOrder{
		Spec: &fulfillmentv1.ClusterOrderSpec{
			TemplateId: c.templateId,
		},
	}

	// Create the order:
	response, err := client.Create(ctx, &fulfillmentv1.ClusterOrdersCreateRequest{
		Object: order,
	})
	if err != nil {
		return fmt.Errorf("failed to create order: %w", err)
	}

	// Display the result:
	order = response.Object
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(writer, "ID: %s\n", order.Id)
	writer.Flush()

	return nil
}
