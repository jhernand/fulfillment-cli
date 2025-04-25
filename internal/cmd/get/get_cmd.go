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
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path"
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

	"github.com/innabox/fulfillment-cli/internal/config"
	"github.com/innabox/fulfillment-cli/internal/logging"
	"github.com/innabox/fulfillment-cli/internal/reflection"
)

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
	return result
}

type runnerContext struct {
	logger         *slog.Logger
	format         string
	conn           *grpc.ClientConn
	marshalOptions protojson.MarshalOptions
	helper         *reflection.ObjectHelper
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
		plurals := helper.Plurals()
		fmt.Printf("You must specify the type of object to get.\n")
		fmt.Printf("\n")
		fmt.Printf("The following object types are available:\n")
		fmt.Printf("\n")
		for _, plural := range plurals {
			fmt.Printf("- %s\n", plural)
		}
		fmt.Printf("\n")
		fmt.Printf("For example, to get the list of cluster templates:\n")
		fmt.Printf("\n")
		fmt.Printf("%s get clustertemplates\n", os.Args[0])
		fmt.Printf("\n")
		fmt.Printf("Use the '--help' option to get more details about the command.\n")
		return nil
	}

	// Get the object helper:
	c.helper = helper.Lookup(args[0])
	if c.helper == nil {
		plurals := helper.Plurals()
		fmt.Printf("There is no object type named '%s'.\n", args[0])
		fmt.Printf("\n")
		fmt.Printf("The following object types are available:\n")
		fmt.Printf("\n")
		for _, plural := range plurals {
			fmt.Printf("- %s\n", plural)
		}
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
	var render func([]proto.Message) error
	switch c.format {
	case outputFormatJson:
		render = c.renderJson
	case outputFormatYaml:
		render = c.renderYaml
	default:
		render = c.renderTable
	}
	return render(objects)
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
	results, err = c.helper.List(ctx)
	return
}

func (c *runnerContext) findTable() (result *Table, err error) {
	entries, err := tablesFS.ReadDir("tables")
	if err != nil {
		err = fmt.Errorf("failed to lookup table metadata for object type '%s': %w", c.helper.FullName(), err)
		return
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".yaml") {
			var table *Table
			table, err = c.loadTable(entry.Name())
			if err != nil {
				return
			}
			if table.FullName == c.helper.FullName() {
				result = table
				return
			}
		}
	}
	return
}

func (c *runnerContext) loadTable(file string) (result *Table, err error) {
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

func (c *runnerContext) renderTable(objects []proto.Message) error {
	// Try to find a table that matches the object type:
	table, err := c.findTable()
	if err != nil {
		return err
	}
	if table == nil {
		table = c.defaultTable()
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
	fmt.Fprintf(writer, "%s\n", c.renderTableHeader(table.Columns))
	for _, object := range objects {
		row, err := c.renderTableRow(table.Columns, prgs, object)
		if err != nil {
			return err
		}
		writer.Write([]byte(row))
	}
	writer.Flush()
	return nil
}

func (c *runnerContext) renderTableHeader(cols []*Column) string {
	headers := make([]string, len(cols))
	for i, col := range cols {
		headers[i] = col.Header
	}
	return strings.Join(headers, "\t")
}

func (c *runnerContext) renderTableRow(cols []*Column, prgs []cel.Program, object proto.Message) (result string,
	err error) {
	in, err := cel.ContextProtoVars(object)
	if err != nil {
		err = fmt.Errorf(
			"failed to set variables for CEL expression for type '%s': %w",
			c.helper, err,
		)
	}
	buffer := &bytes.Buffer{}
	for i := range len(cols) {
		col := cols[i]
		prg := prgs[i]
		var out ref.Val
		out, _, err = prg.Eval(in)
		if err != nil {
			err = fmt.Errorf(
				"failed to evaluate CEL expression '%s' for column '%s' of type '%s': %w",
				col.Value, col.Header, c.helper, err,
			)
			return
		}
		var txt string
		txt, err = c.renderTableCell(col, out)
		if err != nil {
			err = fmt.Errorf(
				"failed to render value '%s' for column '%s' of type '%s': %w",
				out, col.Header, c.helper, err,
			)
			return
		}
		buffer.WriteString(txt)
		if i < len(cols)-1 {
			buffer.WriteRune('\t')
		} else {
			buffer.WriteRune('\n')
		}
	}
	result = buffer.String()
	return
}

func (c *runnerContext) renderTableCell(col *Column, val ref.Val) (result string, err error) {
	switch val := val.(type) {
	case types.Int:
		if col.Type != "" {
			enumType, _ := protoregistry.GlobalTypes.FindEnumByName(col.Type)
			if enumType != nil {
				result, err = c.renderTableCellEnumType(val, enumType.Descriptor())
			}
		} else {
			result, err = c.renderTableCellAnyType(val)
		}
	default:
		result, err = c.renderTableCellAnyType(val)
	}
	return
}

func (c *runnerContext) renderTableCellEnumType(val types.Int, enumDesc protoreflect.EnumDescriptor) (result string,
	err error) {
	// Get the text of the name of the enum value:
	valueDescs := enumDesc.Values()
	valueDesc := valueDescs.ByNumber(protoreflect.EnumNumber(val))
	if valueDesc == nil {
		result = fmt.Sprintf("UNKNOWN:%d", val)
		return
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

	result = valueTxt
	return
}

func (c *runnerContext) renderTableCellAnyType(val ref.Val) (result string, err error) {
	result = fmt.Sprintf("%s", val)
	return
}

func (c *runnerContext) renderJson(objects []proto.Message) error {
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

func (c *runnerContext) renderYaml(objects []proto.Message) error {
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
