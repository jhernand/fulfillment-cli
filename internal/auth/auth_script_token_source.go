/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

// ScriptTokenSourceBuilder contains the logic needed to create a token source that executes an external script to
// generate the token.
type ScriptTokenSourceBuilder struct {
	logger        *slog.Logger
	script        string
	loadTokenFunc func() (string, error)
	saveTokenFunc func(string) error
}

type scriptTokenSource struct {
	logger        *slog.Logger
	script        string
	loadTokenFunc func() (string, error)
	saveTokenFunc func(string) error
	tokenParser   *jwt.Parser
}

// NewScriptTokenSource creates a builder that can then be used to configure and create a token source that executes a
// script to generate the token.
func NewScriptTokenSource() *ScriptTokenSourceBuilder {
	return &ScriptTokenSourceBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *ScriptTokenSourceBuilder) SetLogger(value *slog.Logger) *ScriptTokenSourceBuilder {
	b.logger = value
	return b
}

// SetScript sets script that will be used to generate new tokens. This is mandatory.
func (b *ScriptTokenSourceBuilder) SetScript(value string) *ScriptTokenSourceBuilder {
	b.script = value
	return b
}

// SetTokenLoadFunc sets function that will be used to load the token from the storage. This mandatory.
func (b *ScriptTokenSourceBuilder) SetTokenLoadFunc(value func() (string, error)) *ScriptTokenSourceBuilder {
	b.loadTokenFunc = value
	return b
}

// SetSavefunc sets function that will be used to save the token to the storage. This mandatory.
func (b *ScriptTokenSourceBuilder) SetTokenSaveFunc(value func(string) error) *ScriptTokenSourceBuilder {
	b.saveTokenFunc = value
	return b
}

// Build uses the data stored in the builder to build a new script token source.
func (b *ScriptTokenSourceBuilder) Build() (result oauth2.TokenSource, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.script == "" {
		err = errors.New("token generation script is mandatory")
		return
	}
	if b.loadTokenFunc == nil {
		err = errors.New("token load function is mandatory")
		return
	}
	if b.saveTokenFunc == nil {
		err = errors.New("token save function is mandatory")
		return
	}

	// Create the token parser:
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{
			"RS256",
		}),
		jwt.WithIssuedAt(),
	)

	// Create and populate the object:
	result = &scriptTokenSource{
		logger:        b.logger,
		script:        b.script,
		loadTokenFunc: b.loadTokenFunc,
		saveTokenFunc: b.saveTokenFunc,
		tokenParser:   parser,
	}
	return
}

// Token is the implementation of the oauth2.TokenSource interface.
func (s *scriptTokenSource) Token() (result *oauth2.Token, err error) {
	// If there is no rawToken yet then we need to generate a new one.
	rawToken, err := s.loadTokenFunc()
	if err != nil {
		return
	}
	if rawToken == "" {
		rawToken, err = s.generateToken()
		if err != nil {
			return
		}
	}

	// If the token has expired then we need to generate a new one. Note that if a token isn't a JWT we have no way
	// to check the expiry date, so we don't save it for future use.
	parsedToken, err := s.parseToken(rawToken)
	if err != nil {
		rawToken, err = s.generateToken()
		if err != nil {
			return
		}
		result = &oauth2.Token{
			AccessToken: rawToken,
		}
		err = nil
		return
	}
	if parsedToken.Expiry.Before(time.Now()) {
		rawToken, err = s.generateToken()
		if err != nil {
			return
		}
		parsedToken, err = s.parseToken(rawToken)
		if err != nil {
			result = &oauth2.Token{
				AccessToken: rawToken,
			}
			err = nil
			return
		}
	}
	err = s.saveTokenFunc(rawToken)
	if err != nil {
		return
	}
	result = parsedToken
	return
}

func (s *scriptTokenSource) generateToken() (result string, err error) {
	shell, ok := os.LookupEnv("SHELL")
	if !ok {
		shell = "/usr/bin/sh"
	}
	out := &bytes.Buffer{}
	cmd := exec.Cmd{
		Path: shell,
		Args: []string{
			shell,
			"-c",
			s.script,
		},
		Stdout: out,
	}
	err = cmd.Run()
	if err != nil {
		err = fmt.Errorf("failed to execute token generation script '%s': %w", s.script, err)
		return
	}
	result = strings.TrimSpace(out.String())
	return
}

func (s *scriptTokenSource) parseToken(tokenText string) (result *oauth2.Token, err error) {
	tokenClaims := jwt.MapClaims{}
	_, _, err = s.tokenParser.ParseUnverified(tokenText, tokenClaims)
	if err != nil {
		return
	}
	tokenEpirationTime, err := tokenClaims.GetExpirationTime()
	if err != nil {
		return
	}
	result = &oauth2.Token{
		AccessToken: tokenText,
		Expiry:      tokenEpirationTime.Time,
	}
	return
}
