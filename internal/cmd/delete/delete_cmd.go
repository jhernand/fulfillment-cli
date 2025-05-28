/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package delete

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	"github.com/innabox/fulfillment-cli/internal/config"
	"github.com/innabox/fulfillment-cli/internal/logging"
	"github.com/innabox/fulfillment-cli/internal/reflection"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:   "delete OBJECT [OPTION]... [ID]...",
		Short: "Delete objects",
		RunE:  runner.run,
	}
	return result
}

type runnerContext struct {
	logger *slog.Logger
	conn   *grpc.ClientConn
	helper *reflection.ObjectHelper
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

	// Create the reflection helper:
	helper, err := reflection.NewHelper().
		SetLogger(c.logger).
		SetConnection(c.conn).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create reflection tool: %w", err)
	}

	// Check that the object type has been specified:
	if len(args) == 0 {
		singulars := helper.Singulars()
		fmt.Printf("You must specify the type of object to delete.\n")
		fmt.Printf("\n")
		fmt.Printf("The following object types are available:\n")
		fmt.Printf("\n")
		for _, singular := range singulars {
			fmt.Printf("- %s\n", singular)
		}
		fmt.Printf("\n")
		fmt.Printf("For example, to delete cluster order with identifier '123':\n")
		fmt.Printf("\n")
		fmt.Printf("%s delete clusterorder 123\n", os.Args[0])
		fmt.Printf("\n")
		fmt.Printf("Use the '--help' option to get more details about the command.\n")
		return nil
	}

	// Get the object helper:
	c.helper = helper.Lookup(args[0])
	if c.helper == nil {
		singulars := helper.Singulars()
		fmt.Printf("There is no object type named '%s'.\n", args[0])
		fmt.Printf("\n")
		fmt.Printf("The following object types are available:\n")
		fmt.Printf("\n")
		for _, singular := range singulars {
			fmt.Printf("- %s\n", singular)
		}
		return nil
	}

	// Delete each object specified in the command line:
	for _, id := range args[1:] {
		err = c.helper.Delete(ctx, id)
		if err != nil {
			return fmt.Errorf(
				"failed to delete %s '%s': %w",
				args[0], id, err,
			)
		}
		fmt.Printf("Deleted %s '%s'.\n", args[0], id)
	}

	return nil
}
