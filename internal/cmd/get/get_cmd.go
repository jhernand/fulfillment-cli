/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package get

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
	"gopkg.in/yaml.v3"

	"github.com/innabox/fulfillment-cli/internal/cmd/get/kubeconfig"
	"github.com/innabox/fulfillment-cli/internal/cmd/get/password"
	"github.com/innabox/fulfillment-cli/internal/config"
	"github.com/innabox/fulfillment-cli/internal/logging"
	"github.com/innabox/fulfillment-cli/internal/reflection"
	"github.com/innabox/fulfillment-cli/internal/templating"
	"github.com/innabox/fulfillment-cli/internal/terminal"
)

//go:embed templates
var templatesFS embed.FS

//go:embed tables
var tablesFS embed.FS

// Possible output formats:
const (
	outputFormatTable = "table"
	outputFormatJson  = "json"
	outputFormatYaml  = "yaml"
)

func Cmd() *cobra.Command {
	runner := &runnerContext{
		marshalOptions: protojson.MarshalOptions{
			UseProtoNames: true,
		},
	}
	result := &cobra.Command{
		Use:   "get OBJECT [OPTION]... [ID]...",
		Short: "Get objects",
		RunE:  runner.run,
	}
	result.AddCommand(kubeconfig.Cmd())
	result.AddCommand(password.Cmd())
	flags := result.Flags()
	flags.StringVarP(
		&runner.format,
		"output",
		"o",
		outputFormatTable,
		fmt.Sprintf(
			"Output format, one of '%s', '%s' or '%s'.",
			outputFormatTable, outputFormatJson, outputFormatYaml,
		),
	)
	flags.StringVar(
		&runner.filter,
		"filter",
		"",
		"CEL expression used for filtering results.",
	)
	flags.BoolVar(
		&runner.includeDeleted,
		"include-deleted",
		false,
		"Include deleted objects.",
	)
	return result
}

type runnerContext struct {
	logger         *slog.Logger
	engine         *templating.Engine
	console        *terminal.Console
	format         string
	filter         string
	includeDeleted bool
	conn           *grpc.ClientConn
	marshalOptions protojson.MarshalOptions
	helper         *reflection.ObjectHelper
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	var err error

	// Get the context:
	ctx := cmd.Context()

	// Get the logger and console:
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

	// Check the flags:
	if c.format != outputFormatTable && c.format != outputFormatJson && c.format != outputFormatYaml {
		return fmt.Errorf(
			"unknown output format '%s', should be '%s', '%s' or '%s'",
			c.format, outputFormatTable, outputFormatJson, outputFormatYaml,
		)
	}

	// If there additional arguments in the command line we assume they are identifiers of individual objects, so
	// we need to fetch them using the 'Get' method instead of the 'List' method.
	var objects []proto.Message
	ids := args[1:]
	if len(ids) > 0 {
		objects, err = c.get(ctx, ids)
	} else {
		objects, err = c.list(ctx)
	}
	if err != nil {
		return err
	}

	// Render the items:
	var render func(context.Context, []proto.Message) error
	switch c.format {
	case outputFormatJson:
		render = c.renderJson
	case outputFormatYaml:
		render = c.renderYaml
	default:
		render = c.renderTable
	}
	return render(ctx, objects)
}

func (c *runnerContext) get(ctx context.Context, ids []string) (results []proto.Message, err error) {
	objects := make([]proto.Message, len(ids))
	for i, id := range ids {
		objects[i], err = c.helper.Get(ctx, id)
		if err != nil {
			return
		}
	}
	results = objects
	return
}

func (c *runnerContext) list(ctx context.Context) (results []proto.Message, err error) {
	var options reflection.ListOptions
	if c.filter != "" {
		options.Filter = c.filter
	}
	if !c.includeDeleted {
		const notDeletedFilter = "!has(this.metadata.deletion_timestamp)"
		if options.Filter != "" {
			options.Filter = fmt.Sprintf("%s && (%s)", notDeletedFilter, options.Filter)
		} else {
			options.Filter = notDeletedFilter
		}
	}
	results, err = c.helper.List(ctx, options)
	return
}

func (c *runnerContext) loadTable() (result *Table, err error) {
	file := fmt.Sprintf("%s.yaml", c.helper.FullName())
	data, err := tablesFS.ReadFile(path.Join("tables", file))
	if err != nil {
		err = fmt.Errorf(
			"failed to read table definition file '%s': %w",
			file, err,
		)
		return
	}
	var table Table
	err = yaml.Unmarshal(data, &table)
	if err != nil {
		err = fmt.Errorf(
			"failed to unmarshal table definition file '%s': %w",
			file, err,
		)
		return
	}
	result = &table
	return
}

func (c *runnerContext) defaultTable() *Table {
	return &Table{
		Columns: []*Column{
			{
				Header: "ID",
				Value:  "id",
			},
		},
	}
}

func (c *runnerContext) renderTable(ctx context.Context, objects []proto.Message) error {
	// Check if there are results:
	if len(objects) == 0 {
		c.console.Render(ctx, c.engine, "no_matching_objects.txt", nil)
		return nil
	}

	// Try to load the table that matches the object type:
	table, err := c.loadTable()
	if err != nil {
		return err
	}
	if table == nil {
		table = c.defaultTable()
	}

	// If the user has asked to include deleted objects then add the deletion timesgamp column:
	if c.includeDeleted {
		deletedCol := &Column{
			Header: "DELETED",
			Value:  "has(metadata.deletion_timestamp)? string(metadata.deletion_timestamp): '-'",
		}
		table.Columns = slices.Insert(table.Columns, 1, deletedCol)
	}

	// Compile the CEL programs:
	celEnv, err := cel.NewEnv(
		cel.Types(dynamicpb.NewMessage(c.helper.Descriptor())),
		cel.DeclareContextProto(c.helper.Descriptor()),
	)
	if err != nil {
		return fmt.Errorf("failed to create CEL environment: %w", err)
	}
	prgs := make([]cel.Program, len(table.Columns))
	for i, col := range table.Columns {
		ast, issues := celEnv.Compile(col.Value)
		err = issues.Err()
		if err != nil {
			return fmt.Errorf(
				"failed to compile CEL expression '%s' for column '%s' of type '%s': %w",
				col.Value, col.Header, c.helper, err,
			)
		}
		prg, err := celEnv.Program(ast)
		if err != nil {
			return fmt.Errorf(
				"failed to create CEL program from expression '%s' for column '%s' of type '%s': %w",
				col.Value, col.Header, c.helper, err,
			)
		}
		prgs[i] = prg
	}

	// Render the table:
	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	c.renderTableHeader(writer, table.Columns)
	for _, object := range objects {
		err := c.renderTableRow(writer, table.Columns, prgs, object)
		if err != nil {
			return err
		}
	}
	writer.Flush()

	return nil
}

func (c *runnerContext) renderTableHeader(writer io.Writer, cols []*Column) error {
	for i, col := range cols {
		if i > 0 {
			fmt.Fprint(writer, "\t")
		}
		fmt.Fprintf(writer, "%s", col.Header)
	}
	fmt.Fprintf(writer, "\n")
	return nil
}

func (c *runnerContext) renderTableRow(writer io.Writer, cols []*Column, prgs []cel.Program,
	object proto.Message) error {
	in, err := cel.ContextProtoVars(object)
	if err != nil {
		return fmt.Errorf(
			"failed to set variables for CEL expression for type '%s': %w",
			c.helper, err,
		)
	}
	for i := range len(cols) {
		if i > 0 {
			fmt.Fprintf(writer, "\t")
		}
		if err != nil {
			return err
		}
		col := cols[i]
		prg := prgs[i]
		var out ref.Val
		out, _, err = prg.Eval(in)
		if err != nil {
			return fmt.Errorf(
				"failed to evaluate CEL expression '%s' for column '%s' of type '%s': %w",
				col.Value, col.Header, c.helper, err,
			)
		}
		err = c.renderTableCell(writer, col, out)
		if err != nil {
			return fmt.Errorf(
				"failed to render value '%s' for column '%s' of type '%s': %w",
				out, col.Header, c.helper, err,
			)
		}
	}
	fmt.Fprintf(writer, "\n")
	return nil
}

func (c *runnerContext) renderTableCell(writer io.Writer, col *Column, val ref.Val) error {
	switch val := val.(type) {
	case types.Int:
		if col.Type != "" {
			enumType, _ := protoregistry.GlobalTypes.FindEnumByName(col.Type)
			if enumType != nil {
				return c.renderTableCellEnumType(writer, val, enumType.Descriptor())
			}
		}
	}
	return c.renderTableCellAnyType(writer, val)
}

func (c *runnerContext) renderTableCellEnumType(writer io.Writer, val types.Int,
	enumDesc protoreflect.EnumDescriptor) error {
	// Get the text of the name of the enum value:
	valueDescs := enumDesc.Values()
	valueDesc := valueDescs.ByNumber(protoreflect.EnumNumber(val))
	if valueDesc == nil {
		_, err := fmt.Fprintf(writer, "UNKNOWN:%d", val)
		if err != nil {
			return err
		}
	}
	valueTxt := string(valueDesc.Name())

	// If the enum has been created according to our style guide then all the values should have a prefix with the
	// name of the type, for example `CLUSTER_ORDER_STATUS_STATE`. That prefix is not useful for humans, so we try
	// to remove it. To do so we find the value with number zero, which should end with `_UNSPECIFIED`, extract the
	// prefix from that and remove it from the representation of the value.
	unspecifiedDesc := valueDescs.ByNumber(protoreflect.EnumNumber(0))
	unspecifiedText := string(unspecifiedDesc.Name())
	prefixIndex := strings.LastIndex(unspecifiedText, "_")
	if prefixIndex != -1 {
		prefixTxt := unspecifiedText[0:prefixIndex]
		if strings.HasPrefix(valueTxt, prefixTxt) {
			valueTxt = valueTxt[prefixIndex+1:]
		}
	}

	_, err := fmt.Fprintf(writer, "%s", valueTxt)
	return err
}

func (c *runnerContext) renderTableCellAnyType(writer io.Writer, val ref.Val) error {
	_, err := fmt.Fprintf(writer, "%s", val)
	return err
}

func (c *runnerContext) renderJson(ctx context.Context, objects []proto.Message) error {
	values, err := c.encodeObjects(objects)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if len(values) == 1 {
		return encoder.Encode(values[0])
	}
	return encoder.Encode(values)
}

func (c *runnerContext) renderYaml(ctx context.Context, objects []proto.Message) error {
	values, err := c.encodeObjects(objects)
	if err != nil {
		return err
	}
	encoder := yaml.NewEncoder(os.Stdout)
	encoder.SetIndent(2)
	if len(values) == 1 {
		return encoder.Encode(values[0])
	}
	return encoder.Encode(values)
}

func (c *runnerContext) encodeObjects(objects []proto.Message) (result []any, err error) {
	values := make([]any, len(objects))
	for i, object := range objects {
		values[i], err = c.encodeObject(object)
		if err != nil {
			return
		}
	}
	result = values
	return
}

func (c *runnerContext) encodeObject(object proto.Message) (result any, err error) {
	wrapper, err := anypb.New(object)
	if err != nil {
		return
	}
	var data []byte
	data, err = c.marshalOptions.Marshal(wrapper)
	if err != nil {
		return
	}
	err = json.Unmarshal(data, &result)
	return
}
