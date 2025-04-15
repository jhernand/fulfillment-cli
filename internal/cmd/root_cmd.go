/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cmd

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/innabox/fulfillment-cli/internal/cmd/create"
	"github.com/innabox/fulfillment-cli/internal/cmd/delete"
	"github.com/innabox/fulfillment-cli/internal/cmd/describe"
	"github.com/innabox/fulfillment-cli/internal/cmd/get"
	"github.com/innabox/fulfillment-cli/internal/cmd/getkubeconfig"
	"github.com/innabox/fulfillment-cli/internal/cmd/login"
	"github.com/innabox/fulfillment-cli/internal/cmd/logout"
	"github.com/innabox/fulfillment-cli/internal/logging"
)

func Root() *cobra.Command {
	// create the runner and the command:
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:               "fulfillment-cli",
		Short:             "Command line interface for the fulfillment API",
		SilenceUsage:      true,
		SilenceErrors:     true,
		PersistentPreRunE: runner.persistentPreRun,
	}

	// Add flags:
	logging.AddFlags(result.PersistentFlags())

	// Add commands:
	result.AddCommand(create.Cmd())
	result.AddCommand(delete.Cmd())
	result.AddCommand(describe.Cmd())
	result.AddCommand(get.Cmd())
	result.AddCommand(getkubeconfig.Cmd())
	result.AddCommand(login.Cmd())
	result.AddCommand(logout.Cmd())

	return result
}

type runnerContext struct {
}

func (c *runnerContext) persistentPreRun(cmd *cobra.Command, args []string) error {
	// In order to avoid mixing log messages with output we configure the log to go by default to a file in the user
	// cache directory.
	//
	// The path of the cache directory and of the log file are calculated from the name from the name of the binary.
	// For example, if the name of the binary is `fulfillment-cli` then the cache directory will be
	// `~/.cache/fulfillment-cli` and the log file will be `~/.cache/fufillment-cli/fulfillment-cli.log`.
	baseName := filepath.Base(os.Args[0])
	userCacheDir, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	cacheDir := filepath.Join(userCacheDir, baseName)
	err = os.MkdirAll(cacheDir, 0700)
	if errors.Is(err, os.ErrExist) {
		err = nil
	}
	if err != nil {
		return err
	}
	logFile := filepath.Join(cacheDir, baseName+".log")

	// By the default the logger is configured to write to the log file, and only errors. This Will be overriden by
	// the command line flags.
	logger, err := logging.NewLogger().
		SetFile(logFile).
		SetLevel(slog.LevelError.String()).
		SetFlags(cmd.Flags()).
		Build()
	if err != nil {
		return err
	}

	// Replace the default context with one that contains the logger:
	ctx := cmd.Context()
	ctx = logging.LoggerIntoContext(ctx, logger)
	cmd.SetContext(ctx)

	return nil
}
