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

	ffv1 "github.com/innabox/fulfillment-cli/internal/api/fulfillment/v1"
	"github.com/innabox/fulfillment-cli/internal/config"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:   "cluster [flags]",
		Short: "Create a cluster",
		RunE:  runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.template,
		"template",
		"",
		"Template identifier",
	)
	return result
}

type runnerContext struct {
	template string
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
	if c.template == "" {
		return fmt.Errorf("template identifier is required")
	}

	// Create the gRPC connection from the configuration:
	conn, err := cfg.Connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	// Create the client for the cluster orders service:
	client := ffv1.NewClustersClient(conn)

	// Prepare the cluster:
	cluster := ffv1.Cluster_builder{
		Spec: ffv1.ClusterSpec_builder{
			Template: c.template,
		}.Build(),
	}.Build()

	// Create the cluster:
	response, err := client.Create(ctx, ffv1.ClustersCreateRequest_builder{
		Object: cluster,
	}.Build())
	if err != nil {
		return fmt.Errorf("failed to create: %w", err)
	}

	// Display the result:
	cluster = response.Object
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(writer, "ID: %s\n", cluster.Id)
	writer.Flush()

	return nil
}
