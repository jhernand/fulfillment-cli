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
	"embed"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	"github.com/innabox/fulfillment-cli/internal/config"
	"github.com/innabox/fulfillment-cli/internal/logging"
	"github.com/innabox/fulfillment-cli/internal/reflection"
	"github.com/innabox/fulfillment-cli/internal/templating"
	"github.com/innabox/fulfillment-cli/internal/terminal"
)

//go:embed templates
var templatesFS embed.FS

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
	logger  *slog.Logger
	engine  *templating.Engine
	console *terminal.Console
	conn    *grpc.ClientConn
	helper  *reflection.ObjectHelper
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	var err error

	// Get the context:
	ctx := cmd.Context()

	// Get the logger and the console:
	c.logger = logging.LoggerFromContext(ctx)
	c.console = terminal.ConsoleFromContext(ctx)

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
		AddPackages(cfg.Packages()...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create reflection tool: %w", err)
	}

	// Create the templating engine:
	c.engine, err = templating.NewEngine().
		SetLogger(c.logger).
		SetFS(templatesFS).
		SetDir("templates").
		Build()
	if err != nil {
		return fmt.Errorf("failed to create templating engine: %w", err)
	}

	// Check that the object type has been specified:
	if len(args) == 0 {
		c.console.Render(ctx, c.engine, "no_object.txt", map[string]any{
			"Helper": helper,
			"Binary": os.Args[0],
		})
		return nil
	}

	// Check that at least one object identifier has been specified:
	if len(args) < 2 {
		c.console.Render(ctx, c.engine, "no_id.txt", map[string]any{
			"Binary": os.Args[0],
		})
		return nil
	}

	// Get the object helper:
	c.helper = helper.Lookup(args[0])
	if c.helper == nil {
		c.console.Render(ctx, c.engine, "wrong_object.txt", map[string]any{
			"Helper": helper,
			"Binary": os.Args[0],
			"Object": args[0],
		})
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
