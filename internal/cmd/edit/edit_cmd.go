/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package edit

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	"github.com/innabox/fulfillment-cli/internal/config"
	"github.com/innabox/fulfillment-cli/internal/logging"
	"github.com/innabox/fulfillment-cli/internal/reflection"
	"github.com/innabox/fulfillment-cli/internal/templating"
	"github.com/innabox/fulfillment-cli/internal/terminal"
)

//go:embed templates
var templatesFS embed.FS

// Possible output formats:
const (
	outputFormatJson = "json"
	outputFormatYaml = "yaml"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{
		marshalOptions: protojson.MarshalOptions{
			UseProtoNames: true,
		},
	}
	result := &cobra.Command{
		Use:   "edit OBJECT ID",
		Short: "Edit objects",
		RunE:  runner.run,
	}
	flags := result.Flags()
	flags.StringVarP(
		&runner.format,
		"output",
		"o",
		outputFormatYaml,
		fmt.Sprintf(
			"Output format, one of '%s' or '%s'.",
			outputFormatJson, outputFormatYaml,
		),
	)
	return result
}

type runnerContext struct {
	logger         *slog.Logger
	engine         *templating.Engine
	console        *terminal.Console
	format         string
	conn           *grpc.ClientConn
	marshalOptions protojson.MarshalOptions
	helper         *reflection.ObjectHelper
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

	// Get the information about the object type:
	c.helper = helper.Lookup(args[0])
	if c.helper == nil {
		c.console.Render(ctx, c.engine, "wrong_object.txt", map[string]any{
			"Helper": helper,
			"Binary": os.Args[0],
			"Object": args[0],
		})
		return nil
	}

	// Check the flags:
	if c.format != outputFormatJson && c.format != outputFormatYaml {
		return fmt.Errorf(
			"unknown output format '%s', should be '%s' or '%s'",
			c.format, outputFormatJson, outputFormatYaml,
		)
	}

	// Check that the object identifier has been specified:
	if len(args) < 2 {
		c.console.Render(ctx, c.engine, "no_id.txt", map[string]any{
			"Binary": os.Args[0],
		})
		return nil
	}
	objectId := args[1]

	// Create the gRPC connection from the configuration:
	c.conn, err = cfg.Connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer c.conn.Close()

	// Get the current representation of the object:
	object, err := c.get(ctx, objectId)
	if err != nil {
		return fmt.Errorf("failed to get object of type '%s' with identifier '%s': %w", c.helper, objectId, err)
	}

	// Render the object:
	var render func(proto.Message) ([]byte, error)
	switch c.format {
	case outputFormatJson:
		render = c.renderJson
	default:
		render = c.renderYaml
	}
	data, err := render(object)
	if err != nil {
		return err
	}

	// Write the rendered object to a temporary file:
	tmpDir, err := os.MkdirTemp("", "")
	if err != nil {
		return err
	}
	defer func() {
		err := os.RemoveAll(tmpDir)
		if err != nil {
			c.logger.ErrorContext(
				ctx,
				"Failed to remove temporary directory",
				slog.String("dir", tmpDir),
				slog.Any("error", err),
			)
		}
	}()
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("%s-%s.%s", c.helper, objectId, c.format))
	err = os.WriteFile(tmpFile, data, 0600)
	if err != nil {
		return fmt.Errorf("failed to create temporary file '%s': %w", tmpFile, err)
	}

	// Run the editor:
	editorName := c.findEditor(ctx)
	editorPath, err := exec.LookPath(editorName)
	if err != nil {
		return fmt.Errorf("failed to find editor command '%s': %w", editorName, err)
	}
	editorCmd := &exec.Cmd{
		Path: editorPath,
		Args: []string{
			editorName,
			tmpFile,
		},
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	err = editorCmd.Run()
	if err != nil {
		return fmt.Errorf("failed to edit: %w", err)
	}

	// Load the potentiall modified file:
	data, err = os.ReadFile(tmpFile)
	if err != nil {
		return fmt.Errorf("failed to read back temporary file '%s': %w", tmpFile, err)
	}

	// Parse the result:
	var parse func([]byte) (proto.Message, error)
	switch c.format {
	case outputFormatJson:
		parse = c.parseJson
	default:
		parse = c.parseYaml
	}
	object, err = parse(data)
	if err != nil {
		return fmt.Errorf("failed to parse modified object: %w", err)
	}

	// Save the result:
	_, err = c.update(ctx, object)
	return err
}

// findEditor tries to find the name of the editor command. It will first try with the content of the `EDITOR` and
// `VISUAL` environment variables, and if those are empty it defaults to `vi`.
func (c *runnerContext) findEditor(ctx context.Context) string {
	for _, editorEnvVar := range editorEnvVars {
		value, ok := os.LookupEnv(editorEnvVar)
		if ok && value != "" {
			c.logger.DebugContext(
				ctx,
				"Found editor using environment variable",
				slog.String("var", editorEnvVar),
				slog.String("value", value),
			)
			return value
		}
	}
	c.logger.InfoContext(
		ctx,
		"Didn't find a editor in the environment, will use the default",
		slog.Any("vars", editorEnvVars),
		slog.String("default", defaultEditor),
	)
	return defaultEditor
}

func (c *runnerContext) get(ctx context.Context, id string) (result proto.Message, err error) {
	result, err = c.helper.Get(ctx, id)
	return
}

func (c *runnerContext) update(ctx context.Context, object proto.Message) (result proto.Message, err error) {
	result, err = c.helper.Update(ctx, object)
	return
}

func (c *runnerContext) renderJson(object proto.Message) (result []byte, err error) {
	result, err = c.marshalOptions.Marshal(object)
	return
}

func (c *runnerContext) renderYaml(object proto.Message) (result []byte, err error) {
	data, err := c.renderJson(object)
	if err != nil {
		return
	}
	var value any
	err = json.Unmarshal(data, &value)
	if err != nil {
		return
	}
	buffer := &bytes.Buffer{}
	encoder := yaml.NewEncoder(buffer)
	encoder.SetIndent(2)
	err = encoder.Encode(value)
	if err != nil {
		return
	}
	result = buffer.Bytes()
	return
}

func (c *runnerContext) parseJson(data []byte) (result proto.Message, err error) {
	object := c.helper.Instance()
	err = protojson.Unmarshal(data, object)
	if err != nil {
		return
	}
	result = object
	return
}

func (c *runnerContext) parseYaml(data []byte) (result proto.Message, err error) {
	var value any
	err = yaml.Unmarshal(data, &value)
	if err != nil {
		return
	}
	data, err = json.Marshal(value)
	if err != nil {
		return
	}
	result, err = c.parseJson(data)
	return
}

// editorEnvVars is the list of environment variables that will be used to obtain the name of the editor command.
var editorEnvVars = []string{
	"EDITOR",
	"VISUAL",
}

// defualtEditor is the editor used when the environment variables don't indicate any other editor.
const defaultEditor = "vi"
