/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package config

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/innabox/fulfillment-cli/internal/auth"
	"github.com/innabox/fulfillment-cli/internal/logging"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/credentials/oauth"

	experimentalcredentials "google.golang.org/grpc/experimental/credentials"
)

// Config is the type used to store the configuration of the client.
type Config struct {
	Token       string `json:"token,omitempty"`
	TokenScript string `json:"token_script"`
	Plaintext   bool   `json:"plaintext,omitempty"`
	Insecure    bool   `json:"insecure,omitempty"`
	Address     string `json:"address,omitempty"`
}

// Load loads the configuration from the configuration file.
func Load() (cfg *Config, err error) {
	file, err := Location()
	if err != nil {
		return
	}
	_, err = os.Stat(file)
	if os.IsNotExist(err) {
		cfg = &Config{}
		err = nil
		return
	}
	if err != nil {
		err = fmt.Errorf("failed to check if config file '%s' exists: %v", file, err)
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		err = fmt.Errorf("failed to read config file '%s': %v", file, err)
		return
	}
	cfg = &Config{}
	if len(data) == 0 {
		return
	}
	err = json.Unmarshal(data, cfg)
	if err != nil {
		err = fmt.Errorf("failed to parse config file '%s': %v", file, err)
		return
	}
	return
}

// Save saves the given configuration to the configuration file.
func Save(cfg *Config) error {
	file, err := Location()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}
	dir := filepath.Dir(file)
	err = os.MkdirAll(dir, os.FileMode(0755))
	if err != nil {
		return fmt.Errorf("failed to create directory %s: %v", dir, err)
	}
	err = os.WriteFile(file, data, 0600)
	if err != nil {
		return fmt.Errorf("failed to write file '%s': %v", file, err)
	}
	return nil
}

// Location returns the location of the configuration file.
func Location() (result string, err error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	result = filepath.Join(configDir, "fulfillment-cli", "config.json")
	return
}

// Connect creates a gRPC connection from the configuration.
func (c *Config) Connect(ctx context.Context) (result *grpc.ClientConn, err error) {
	var dialOpts []grpc.DialOption

	// Get the logger:
	logger := logging.LoggerFromContext(ctx)

	// Configure use of TLS:
	var transportCreds credentials.TransportCredentials
	if c.Plaintext {
		transportCreds = insecure.NewCredentials()
	} else {
		tlsConfig := &tls.Config{}
		if c.Insecure {
			tlsConfig.InsecureSkipVerify = true
		}

		// TODO: This should have been the non-experimental package, but we need to use this one because
		// currently the OpenShift router doesn't seem to support ALPN, and the regular credentials package
		// requires it since version 1.67. See here for details:
		//
		// https://github.com/grpc/grpc-go/issues/434
		// https://github.com/grpc/grpc-go/pull/7980
		//
		// Is there a way to configure the OpenShift router to avoid this?
		transportCreds = experimentalcredentials.NewTLSWithALPNDisabled(tlsConfig)
	}
	if transportCreds != nil {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(transportCreds))
	}

	// Confgure use of token:
	var tokenSource oauth2.TokenSource
	if c.TokenScript != "" {
		tokenSource, err = auth.NewScriptTokenSource().
			SetLogger(logger).
			SetScript(c.TokenScript).
			SetTokenLoadFunc(func() (token string, err error) {
				token = c.Token
				return
			}).
			SetTokenSaveFunc(func(token string) error {
				c.Token = token
				return Save(c)
			}).
			Build()
		if err != nil {
			return
		}
	} else if c.Token != "" {
		token := &oauth2.Token{
			AccessToken: c.Token,
		}
		tokenSource = oauth.TokenSource{
			TokenSource: oauth2.StaticTokenSource(token),
		}
	}
	if tokenSource != nil {
		token := oauth.TokenSource{
			TokenSource: tokenSource,
		}
		dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(token))
	}

	result, err = grpc.NewClient(c.Address, dialOpts...)
	return
}
