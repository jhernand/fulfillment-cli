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
	"os"
	"path"
	"strings"
	"text/tabwriter"

	"github.com/gertd/go-pluralize"
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
		pluralizer: pluralize.NewClient(),
		marshalOptions: protojson.MarshalOptions{
			UseProtoNames: true,
		},
	}
	result := &cobra.Command{
		Use:   "get OBJECT [OPTION]... [ID]...",
		Short: "Get objects",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runner.run,
	}
	flags := result.Flags()
	flags.StringVarP(
		&runner.outputFormat,
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
	outputFormat         string
	grpcConn             *grpc.ClientConn
	marshalOptions       protojson.MarshalOptions
	pluralizer           *pluralize.Client
	objectType           string
	objectDesc           protoreflect.MessageDescriptor
	serviceDesc          protoreflect.ServiceDescriptor
	listMethodDesc       protoreflect.MethodDescriptor
	listMethodPath       string
	listRequestTemplate  proto.Message
	listResponseTemplate proto.Message
	listItemsFieldDesc   protoreflect.FieldDescriptor
	getMethodDesc        protoreflect.MethodDescriptor
	getMethodPath        string
	getRequestTemplate   proto.Message
	getResponseTemplate  proto.Message
	getIdFieldDesc       protoreflect.FieldDescriptor
	getObjectFieldDesc   protoreflect.FieldDescriptor
}

func (c *runnerContext) run(cmd *cobra.Command, args []string) error {
	// Get the context:
	ctx := cmd.Context()

	// Check the flags:
	if c.outputFormat != outputFormatTable && c.outputFormat != outputFormatJson && c.outputFormat != outputFormatYaml {
		return fmt.Errorf(
			"unknown output format '%s', should be '%s', '%s' or '%s'",
			c.outputFormat, outputFormatTable, outputFormatJson, outputFormatYaml,
		)
	}

	// Get the configuration:
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("there is no configuration, run the 'login' command")
	}

	// Create the gRPC connection from the configuration:
	c.grpcConn, err = cfg.Connect()
	if err != nil {
		return fmt.Errorf("failed to create gRPC connection: %w", err)
	}
	defer c.grpcConn.Close()

	// Analyze the protocol buffers descriptors to find the information that we need, like the name of the method,
	// the request and response types, and the type of the items.
	c.objectType = args[0]
	err = c.analyzeDescriptors()
	if err != nil {
		return err
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
	switch c.outputFormat {
	case outputFormatJson:
		render = c.renderJson
	case outputFormatYaml:
		render = c.renderYaml
	default:
		render = c.renderTable
	}
	return render(objects)
}

func (c *runnerContext) list(ctx context.Context) (result []proto.Message, err error) {
	request := proto.Clone(c.listRequestTemplate)
	response := proto.Clone(c.listResponseTemplate)
	err = c.grpcConn.Invoke(ctx, c.listMethodPath, request, response)
	if err != nil {
		return
	}
	list := response.ProtoReflect().Get(c.listItemsFieldDesc).List()
	result = make([]proto.Message, list.Len())
	for i := range list.Len() {
		result[i] = list.Get(i).Message().Interface()
	}
	return
}

func (c *runnerContext) get(ctx context.Context, ids []string) (result []proto.Message, err error) {
	objects := make([]proto.Message, len(ids))
	for i, id := range ids {
		request := proto.Clone(c.getRequestTemplate)
		request.ProtoReflect().Set(c.getIdFieldDesc, protoreflect.ValueOfString(id))
		response := proto.Clone(c.getResponseTemplate)
		err = c.grpcConn.Invoke(ctx, c.getMethodPath, request, response)
		if err != nil {
			err = fmt.Errorf("failed to get object with identifier '%s': %w", id, err)
		}
		objects[i] = response.ProtoReflect().Get(c.getObjectFieldDesc).Message().Interface()
	}
	result = objects
	return
}

func (c *runnerContext) analyzeDescriptors() error {
	finders := []func() error{
		c.findTypeDesc,
		c.findServiceDesc,
		c.findListMethodDescs,
		c.findGetMethodDescs,
	}
	for _, finder := range finders {
		err := finder()
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *runnerContext) findTypeDesc() error {
	// In order to find the object type we try to find a protobuf message that has a name that directly or converted
	// to plural matches the given resource type.
	protoregistry.GlobalTypes.RangeMessages(
		func(messageType protoreflect.MessageType) bool {
			currentDesc := messageType.Descriptor()
			currentSingular := string(currentDesc.Name())
			currentPlural := c.pluralizer.Plural(currentSingular)
			matchesSingular := strings.EqualFold(currentSingular, c.objectType)
			matchesPlural := strings.EqualFold(currentPlural, c.objectType)
			if matchesSingular || matchesPlural {
				c.objectDesc = currentDesc
				return false
			}
			return true
		},
	)
	if c.objectDesc == nil {
		return fmt.Errorf("failed to find a descriptor matching object type '%s'", c.objectType)
	}
	return nil
}

func (c *runnerContext) findServiceDesc() error {
	// In order to find the service we assume that its name is the plural of the name of the object type. For
	// example, if the name of the object type is `Cluster` we expect the name of the service to be `Clusters`. Then
	// we need to iterate all the files of the package, because the protobuf reflection library doesn't provide a
	// mechanism to directly lookup a service by name.
	objectFullName := c.objectDesc.FullName()
	packageName := objectFullName.Parent()
	objectName := objectFullName.Name()
	serviceName := protoreflect.Name(c.pluralizer.Plural(string(objectName)))
	protoregistry.GlobalFiles.RangeFilesByPackage(
		packageName,
		func(fileDesc protoreflect.FileDescriptor) bool {
			currentDesc := fileDesc.Services().ByName(serviceName)
			if currentDesc != nil {
				c.serviceDesc = currentDesc
				return false
			}
			return true
		},
	)
	if c.serviceDesc == nil {
		return fmt.Errorf("failed to find service matching object type '%s'", c.objectType)
	}
	return nil
}

func (c *runnerContext) findListMethodDescs() error {
	var err error

	// Find the method:
	c.listMethodDesc = c.serviceDesc.Methods().ByName(protoreflect.Name("List"))
	if c.listMethodDesc == nil {
		return fmt.Errorf("failed to find list method for service '%s'", c.serviceDesc.FullName())
	}
	c.listMethodPath = c.makeMethodPath(c.listMethodDesc)

	// Create templates for the request and response messages:
	c.listRequestTemplate, c.listResponseTemplate, err = c.makeMethodTemplates(c.listMethodDesc)
	if err != nil {
		return err
	}

	// Find the items field of the response message:
	responseDesc := c.listMethodDesc.Output()
	c.listItemsFieldDesc = responseDesc.Fields().ByName("items")
	if c.listItemsFieldDesc == nil {
		return fmt.Errorf(
			"failed to find items field for response type '%s' of list method of service '%s'",
			responseDesc.FullName(), c.serviceDesc.FullName(),
		)
	}
	if !c.listItemsFieldDesc.IsList() {
		return fmt.Errorf(
			"items field of response type '%s' of list method of service '%s' isn't a list",
			responseDesc.FullName(), c.serviceDesc.FullName(),
		)
	}

	return nil
}

func (c *runnerContext) findGetMethodDescs() error {
	var err error

	// Find the method:
	c.getMethodDesc = c.serviceDesc.Methods().ByName(protoreflect.Name("Get"))
	if c.getMethodDesc == nil {
		return fmt.Errorf("failed to find get method for service '%s'", c.serviceDesc.FullName())
	}
	c.getMethodPath = c.makeMethodPath(c.getMethodDesc)

	// Create templates for the request and response messages:
	c.getRequestTemplate, c.getResponseTemplate, err = c.makeMethodTemplates(c.getMethodDesc)
	if err != nil {
		return err
	}

	// Find the identifier field of the request type:
	requestDesc := c.getMethodDesc.Input()
	c.getIdFieldDesc = requestDesc.Fields().ByName("id")
	if c.getIdFieldDesc == nil {
		return fmt.Errorf(
			"failed to find identifier field for request type '%s' of get method of service '%s'",
			requestDesc.FullName(), c.serviceDesc.FullName(),
		)
	}

	// Find the object field of the response type:
	responseDesc := c.getMethodDesc.Output()
	c.getObjectFieldDesc = responseDesc.Fields().ByName("object")
	if c.getObjectFieldDesc == nil {
		return fmt.Errorf(
			"failed to find object field for response type '%s' of get method of service '%s'",
			responseDesc.FullName(), c.serviceDesc.FullName(),
		)
	}

	return nil
}

func (c *runnerContext) makeMethodPath(methodDesc protoreflect.MethodDescriptor) string {
	return fmt.Sprintf("/%s/%s", methodDesc.FullName().Parent(), methodDesc.Name())
}

func (c *runnerContext) makeMethodTemplates(methodDesc protoreflect.MethodDescriptor) (requestTemplate,
	responseTemplate proto.Message, err error) {
	// Find the request type:
	requestDesc := methodDesc.Input()
	requestName := requestDesc.FullName()
	requestType, err := protoregistry.GlobalTypes.FindMessageByName(requestName)
	if err != nil {
		err = fmt.Errorf(
			"failed to find request type '%s' for method '%s': %w",
			requestName, methodDesc.Name(), err,
		)
		return
	}

	// Find the response type:
	responseDesc := methodDesc.Output()
	responseName := responseDesc.FullName()
	responseType, err := protoregistry.GlobalTypes.FindMessageByName(responseName)
	if err != nil {
		err = fmt.Errorf(
			"failed to find response type '%s' for method '%s': %w",
			requestName, c.serviceDesc.FullName(), err,
		)
		return
	}

	// Create the templates:
	requestTemplate = requestType.New().Interface()
	responseTemplate = responseType.New().Interface()
	return
}

func (c *runnerContext) findTable() (result *Table, err error) {
	entries, err := tablesFS.ReadDir("tables")
	if err != nil {
		err = fmt.Errorf("failed to lookup table metadata for type '%s': %w", c.objectDesc.FullName(), err)
		return
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".yaml") {
			var table *Table
			table, err = c.loadTable(entry.Name())
			if err != nil {
				return
			}
			if table.FullName == c.objectDesc.FullName() {
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
		cel.Types(dynamicpb.NewMessage(c.objectDesc)),
		cel.DeclareContextProto(c.objectDesc),
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
				col.Value, col.Header, c.objectDesc.FullName(), err,
			)
		}
		prg, err := celEnv.Program(ast)
		if err != nil {
			return fmt.Errorf(
				"failed to create CEL program from expression '%s' for column '%s' of type '%s': %w",
				col.Value, col.Header, c.objectDesc.FullName(), err,
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
			c.objectDesc.FullName(), err,
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
				col.Value, col.Header, c.objectDesc.FullName(), err,
			)
			return
		}
		var txt string
		txt, err = c.renderTableCell(col, out)
		if err != nil {
			err = fmt.Errorf(
				"failed to render value '%s' for column '%s' of type '%s': %w",
				out, col.Header, c.objectDesc.FullName(), err,
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
