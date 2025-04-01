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
	"github.com/spf13/cobra"

	"github.com/innabox/fulfillment-cli/internal/cmd/create"
	"github.com/innabox/fulfillment-cli/internal/cmd/get"
	"github.com/innabox/fulfillment-cli/internal/cmd/login"
	"github.com/innabox/fulfillment-cli/internal/cmd/logout"
)

func Root() *cobra.Command {
	result := &cobra.Command{
		Use:           "fulfillment-cli",
		Short:         "Command line interface for the fulfillment API",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	result.AddCommand(create.Cmd())
	result.AddCommand(get.Cmd())
	result.AddCommand(login.Cmd())
	result.AddCommand(logout.Cmd())
	return result
}
