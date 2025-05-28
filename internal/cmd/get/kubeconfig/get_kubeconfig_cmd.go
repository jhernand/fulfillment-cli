/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package kubeconfig

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	ffv1 "github.com/innabox/fulfillment-cli/internal/api/fulfillment/v1"
	"github.com/innabox/fulfillment-cli/internal/config"
	"github.com/innabox/fulfillment-cli/internal/logging"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:   "kubeconfig [OPTION]...",
		Short: "Get kubeconfig",
		RunE:  runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.cluster,
		"cluster",
		"",
		"Identifier of the cluster. This is mandatory.",
	)
	return result
}

type runnerContext struct {
	logger  *slog.Logger
	cluster string
	conn    *grpc.ClientConn
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	var err error

	// Get the context:
	ctx := cmd.Context()

	// Get the logger:
	c.logger = logging.LoggerFromContext(ctx)

	// Get the configuration:
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Create the gRPC connection from the configuration:
	c.conn, err = cfg.Connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer c.conn.Close()

	// Check the flags:
	if c.cluster == "" {
		return fmt.Errorf("it is mandatory to specify the cluster identifier with the '--cluster' option")
	}

	// Get the kubeconfig:
	client := ffv1.NewClustersClient(c.conn)
	response, err := client.GetKubeconfig(ctx, ffv1.ClustersGetKubeconfigRequest_builder{
		Id: c.cluster,
	}.Build())
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", response.Kubeconfig)

	return nil
}
