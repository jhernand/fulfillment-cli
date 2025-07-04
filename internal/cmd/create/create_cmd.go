/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package create

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"gopkg.in/yaml.v3"

	"github.com/innabox/fulfillment-cli/internal/cmd/create/cluster"
	"github.com/innabox/fulfillment-cli/internal/cmd/create/hub"
	"github.com/innabox/fulfillment-cli/internal/config"
	"github.com/innabox/fulfillment-cli/internal/logging"
	"github.com/innabox/fulfillment-cli/internal/reflection"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{}
	result := &cobra.Command{
		Use:   "create [OPTION]...",
		Short: "Create objects",
		RunE:  runner.run,
	}
	result.AddCommand(cluster.Cmd())
	result.AddCommand(hub.Cmd())
	flags := result.Flags()
	flags.StringVarP(
		&runner.fileName,
		"filename",
		"f",
		"",
		"Name of the file containg the object to create. This is mandatory. If the value is '-' the object is "+
			"read from the standard input.",
	)
	return result
}

type runnerContext struct {
	logger   *slog.Logger
	fileName string
	conn     *grpc.ClientConn
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
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
		AddPackages(cfg.Packages()...).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create reflection tool: %w", err)
	}

	// Check the flags:
	if c.fileName == "" {
		return fmt.Errorf("it is mandatory to specify the input file with the '--filename' or '-f' options")
	}

	// Open the input:
	var reader io.ReadCloser
	if c.fileName == "-" {
		reader = os.Stdin
	} else {
		reader, err = os.Open(c.fileName)
		if err != nil {
			return fmt.Errorf("failed to open the file '%s': %w", c.fileName, err)
		}
		defer func() {
			reader.Close()
			if err != nil {
				c.logger.LogAttrs(
					ctx,
					slog.LevelError,
					"Failed to close file",
					slog.String("file", c.fileName),
					slog.Any("error", err),
				)
			}
		}()
	}

	// Convert the input to a list of objects, and then create them:
	objects, err := c.decodeObjects(reader)
	if err != nil {
		return err
	}
	for i, object := range objects {
		objectDesc := object.ProtoReflect().Descriptor()
		objectName := string(objectDesc.FullName())
		objectHelper := helper.Lookup(objectName)
		if objectHelper == nil {
			return fmt.Errorf("input object at index %d is of an unknown type '%s'", i, objectName)
		}
		object, err = objectHelper.Create(ctx, object)
		if err != nil {
			return fmt.Errorf("failed to create object at index %d: %w", i, err)
		}
		type objectIface interface {
			GetId() string
		}
		objectId := object.(objectIface).GetId()
		fmt.Printf("Created %s '%s'.\n", objectHelper.Singular(), objectId)
	}

	return nil
}

// decode reads the given input, which may contain multiple YAML or JSON documents, each of them being a single object
// or alist, and returns the corresponding list of protocol buffers messages
func (c *runnerContext) decodeObjects(input io.Reader) (result []proto.Message, err error) {
	// Parse the input file assuming it is a YAML file. As JSON is a subset of YAML, this will also work for JSON.
	decoder := yaml.NewDecoder(input)
	var items []any
	for {
		var item any
		err = decoder.Decode(&item)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return
		}
		items = append(items, item)
	}

	// Items may be a single object or a list of objects. Those that are a list need to be converted to single
	// objects.
	list := make([]any, 0, len(items))
	for _, item := range items {
		switch item := item.(type) {
		case []any:
			list = append(list, item...)
		default:
			list = append(list, item)
		}
	}

	// We assume that input objects are protocol buffers any objects, and we need to convert them to the
	// appropriate type.
	objects := make([]proto.Message, len(list))
	for i, item := range list {
		var data []byte
		data, err = json.Marshal(item)
		if err != nil {
			err = fmt.Errorf("failed to convert item at index %d to JSON: %w", i, err)
			return
		}
		value := &anypb.Any{}
		err = protojson.Unmarshal(data, value)
		if err != nil {
			err = fmt.Errorf(
				"failed to unmarshal item at index %d to a protocol buffers any: %w",
				i, err,
			)
			return
		}
		var object proto.Message
		object, err = value.UnmarshalNew()
		if err != nil {
			err = fmt.Errorf(
				"failed to unmarshal object at index %d to a protocol buffers object: %w",
				i, err,
			)
			return
		}
		objects[i] = object
	}

	result = objects
	return
}
