/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package login

import (
	"fmt"

	"github.com/innabox/fulfillment-cli/internal/config"
	"github.com/spf13/cobra"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:   "login [flags]",
		Short: "Save connection and authentication details",
		RunE:  runner.run,
	}
	flags := result.Flags()
	flags.StringVar(
		&runner.token,
		"token",
		"",
		"Authentication token",
	)
	flags.StringVar(
		&runner.tokenScript,
		"token-script",
		"",
		"Shell command that will be executed to obtain the token. For example, to automatically get the "+
			"token of the Kubernetes 'client' service account of the 'example' namespace the value "+
			"could be 'kubectl create token -n example client --duration 1h'. Note that is important "+
			"to quote this shell command correctly, as it will be passed to your shell for "+
			"execution.",
	)
	flags.BoolVar(
		&runner.plaintext,
		"plaintext",
		false,
		"Disables use of TLS for communications",
	)
	flags.BoolVar(
		&runner.insecure,
		"insecure",
		false,
		"Disables verification of TLS certificates and host names",
	)
	flags.StringVar(
		&runner.address,
		"address",
		"",
		"Server address",
	)
	return result
}

type runnerContext struct {
	token       string
	tokenScript string
	plaintext   bool
	insecure    bool
	address     string
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	// Load the configuration:
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}
	if cfg == nil {
		cfg = &config.Config{}
	}

	// Check mandatory parameters:
	if c.address == "" {
		return fmt.Errorf("address is mandatory")
	}

	// Update the configuration with the values given in the command line:
	cfg.Token = c.token
	cfg.TokenScript = c.tokenScript
	cfg.Plaintext = c.plaintext
	cfg.Insecure = c.insecure
	cfg.Address = c.address

	// Save the configuration:
	err = config.Save(cfg)
	if err != nil {
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	return nil
}
