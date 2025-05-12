/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package reflection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/gertd/go-pluralize"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Frequently used names:
const (
	// Methods:
	deleteMethodName = protoreflect.Name("Delete")
	getMethodName    = protoreflect.Name("Get")
	listMethodName   = protoreflect.Name("List")
	updateMethodName = protoreflect.Name("Update")

	// Fields:
	filterFieldName = protoreflect.Name("filter")
	idFieldName     = protoreflect.Name("id")
	itemsFieldName  = protoreflect.Name("items")
	objectFieldName = protoreflect.Name("object")
)

// HelperBuilder contains the data and logic needed to create a reflection helper.
//
// Don't create instances of this type directly, use the NewHelper function instead.
type HelperBuilder struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
}

// Helper simplifies use of the protocol buffers reflection facility. It knows how to extract from the descriptors the
// list of message types that satisfy the conditions to be considered objects, as well as the services that support them
// and the methods to get, list, update and delete instances.
//
// Don't create instances of this type directly, use the NewHelper function instead.
type Helper struct {
	logger     *slog.Logger
	connection *grpc.ClientConn
	scanOnce   *sync.Once
	pluralizer *pluralize.Client
	helpers    []ObjectHelper
}

// NewHelper creates a builder that can then be used to configure a reflection helper.
func NewHelper() *HelperBuilder {
	return &HelperBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *HelperBuilder) SetLogger(value *slog.Logger) *HelperBuilder {
	b.logger = value
	return b
}

// SetConnection sets the gRPC connection that will be used to invoke mehods. This is mandatory.
func (b *HelperBuilder) SetConnection(value *grpc.ClientConn) *HelperBuilder {
	b.connection = value
	return b
}

// Build uses the data stored in the builder to create a new reflection helper.
func (b *HelperBuilder) Build() (result *Helper, err error) {
	// Check the parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.connection == nil {
		err = errors.New("gRPC connection is mandatory")
		return
	}

	// Create the pluralizer:
	pluralizer := pluralize.NewClient()

	// Create and populate the object:
	result = &Helper{
		logger:     b.logger,
		connection: b.connection,
		pluralizer: pluralizer,
		scanOnce:   &sync.Once{},
		helpers:    []ObjectHelper{},
	}
	return
}

func (h *Helper) scanIfNeeded() {
	h.scanOnce.Do(func() {
		h.scan()
	})
}

func (h *Helper) scan() {
	protoregistry.GlobalFiles.RangeFiles(h.scanFile)
}

func (h *Helper) scanFile(fileDesc protoreflect.FileDescriptor) bool {
	h.logger.Debug(
		"Scanning file",
		slog.String("file", fileDesc.Path()),
	)
	serviceDescs := fileDesc.Services()
	for i := range serviceDescs.Len() {
		h.scanService(serviceDescs.Get(i))
	}
	return true
}

func (h *Helper) scanService(serviceDesc protoreflect.ServiceDescriptor) {
	// The service must have the get, list, update and delete method:
	h.logger.Debug(
		"Scanning service",
		slog.String("service", string(serviceDesc.FullName())),
	)
	methodDescs := serviceDesc.Methods()
	listDesc := methodDescs.ByName(listMethodName)
	if listDesc == nil {
		return
	}
	getDesc := methodDescs.ByName(getMethodName)
	if getDesc == nil {
		return
	}
	updateDesc := methodDescs.ByName(updateMethodName)
	if updateDesc == nil {
		return
	}
	deleteDesc := methodDescs.ByName(deleteMethodName)
	if deleteDesc == nil {
		return
	}

	// The request of the get method must have an `id` field:
	getRequestIdFieldDesc := h.getIdField(getDesc.Input())
	if getRequestIdFieldDesc == nil {
		return
	}

	// The response of the get method must have an `object` field:
	getResponseObjectFieldDesc := h.getObjectField(getDesc.Output())
	objectDesc := getResponseObjectFieldDesc.Message()

	// The request of the list method must have a `filter` field:
	listRequestFilterFieldDesc := h.getFilterField(listDesc.Input())
	if listRequestFilterFieldDesc == nil {
		return
	}

	// The response of the list method must have an `items` field:
	listResponseItemsFieldDesc := h.getItemsField(listDesc.Output())
	if listResponseItemsFieldDesc == nil {
		return
	}
	if listResponseItemsFieldDesc.Message() != objectDesc {
		return
	}

	// The request and response of the `Update` method must have an `object` message field:
	updateRequestObjectFieldDesc := h.getObjectField(updateDesc.Input())
	if updateRequestObjectFieldDesc == nil {
		return
	}
	if updateRequestObjectFieldDesc.Message() != objectDesc {
		return
	}
	updateResponseObjectFieldDesc := h.getObjectField(updateDesc.Output())
	if updateResponseObjectFieldDesc == nil {
		return
	}
	if updateResponseObjectFieldDesc.Message() != objectDesc {
		return
	}

	// The request of the `Delete` method must have an `id` string field:
	deleteRequestIdFieldDesc := h.getIdField(deleteDesc.Input())
	if deleteRequestIdFieldDesc == nil {
		return
	}

	// Create the object template:
	objectTemplate := h.makeTemplate(objectDesc)

	// Create templates for the request and response messages:
	getRequestTemplate, getResponseTemplate := h.makeMethodTemplates(getDesc)
	listRequestTemplate, listResponseTemplate := h.makeMethodTemplates(listDesc)
	updateRequestTemplate, updateResponseTemplate := h.makeMethodTemplates(updateDesc)
	deleteRequestTemplate, deleteResponseTemplate := h.makeMethodTemplates(deleteDesc)

	// This is a supported object type:
	helper := ObjectHelper{
		parent:     h,
		descriptor: objectDesc,
		template:   objectTemplate,
		get: getInfo{
			methodInfo: methodInfo{
				path:     h.makeMethodPath(getDesc),
				request:  getRequestTemplate,
				response: getResponseTemplate,
			},
			id:     getRequestIdFieldDesc,
			object: getResponseObjectFieldDesc,
		},
		list: listInfo{
			methodInfo: methodInfo{
				path:     h.makeMethodPath(listDesc),
				request:  listRequestTemplate,
				response: listResponseTemplate,
			},
			filter: listRequestFilterFieldDesc,
			items:  listResponseItemsFieldDesc,
		},
		update: updateInfo{
			methodInfo: methodInfo{
				path:     h.makeMethodPath(updateDesc),
				request:  updateRequestTemplate,
				response: updateResponseTemplate,
			},
			in:  updateRequestObjectFieldDesc,
			out: updateResponseObjectFieldDesc,
		},
		delete: deleteInfo{
			methodInfo: methodInfo{
				path:     h.makeMethodPath(deleteDesc),
				request:  deleteRequestTemplate,
				response: deleteResponseTemplate,
			},
			id: deleteRequestIdFieldDesc,
		},
	}
	h.helpers = append(h.helpers, helper)
}

func (h *Helper) getIdField(messageDesc protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	fieldDesc := messageDesc.Fields().ByName(idFieldName)
	if fieldDesc == nil {
		return nil
	}
	if fieldDesc.Cardinality() != protoreflect.Optional {
		return nil
	}
	if fieldDesc.Kind() != protoreflect.StringKind {
		return nil
	}
	return fieldDesc
}

func (h *Helper) getObjectField(messageDesc protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	fieldDesc := messageDesc.Fields().ByName(objectFieldName)
	if fieldDesc == nil {
		return nil
	}
	if fieldDesc.Cardinality() != protoreflect.Optional {
		return nil
	}
	if fieldDesc.Kind() != protoreflect.MessageKind {
		return nil
	}
	return fieldDesc
}

func (h *Helper) getFilterField(messageDesc protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	fieldDesc := messageDesc.Fields().ByName(filterFieldName)
	if fieldDesc == nil {
		return nil
	}
	if fieldDesc.Cardinality() == protoreflect.Repeated {
		return nil
	}
	if fieldDesc.Kind() != protoreflect.StringKind {
		return nil
	}
	return fieldDesc
}

func (h *Helper) getItemsField(messageDesc protoreflect.MessageDescriptor) protoreflect.FieldDescriptor {
	fieldDesc := messageDesc.Fields().ByName(itemsFieldName)
	if fieldDesc == nil {
		return nil
	}
	if fieldDesc.Cardinality() != protoreflect.Repeated {
		return nil
	}
	if fieldDesc.Kind() != protoreflect.MessageKind {
		return nil
	}
	return fieldDesc
}

// Singulars returns the object types in singular. The results are in lower case and sorted alphabetically.
func (h *Helper) Singulars() []string {
	h.scanIfNeeded()
	results := make([]string, len(h.helpers))
	for i, objectInfo := range h.helpers {
		results[i] = strings.ToLower(string(objectInfo.descriptor.Name()))
	}
	sort.Strings(results)
	return results
}

// Plurals the object types in plural. The reusults are in lower case an sorted alphabetically.
func (a *Helper) Plurals() []string {
	a.scanIfNeeded()
	results := make([]string, len(a.helpers))
	for i, objectInfo := range a.helpers {
		results[i] = a.pluralizer.Plural(strings.ToLower(string(objectInfo.descriptor.Name())))
	}
	sort.Strings(results)
	return results
}

// Lookup returns the helper for the given object type. Returns nil if there is no such object.
func (a *Helper) Lookup(objectType string) *ObjectHelper {
	a.scanIfNeeded()
	for i, objectInfo := range a.helpers {
		singular := strings.ToLower(string(objectInfo.descriptor.Name()))
		if strings.EqualFold(objectType, singular) {
			return &a.helpers[i]
		}
		plural := a.pluralizer.Plural(singular)
		if strings.EqualFold(objectType, plural) {
			return &a.helpers[i]
		}
	}
	return nil
}

func (h *Helper) makeMethodPath(methodDesc protoreflect.MethodDescriptor) string {
	return fmt.Sprintf("/%s/%s", methodDesc.FullName().Parent(), methodDesc.Name())
}

func (h *Helper) makeMethodTemplates(methodDesc protoreflect.MethodDescriptor) (requestTemplate,
	responseTemplate proto.Message) {
	requestTemplate = h.makeTemplate(methodDesc.Input())
	responseTemplate = h.makeTemplate(methodDesc.Output())
	return
}

func (h *Helper) makeTemplate(messageDesc protoreflect.MessageDescriptor) proto.Message {
	messageType, err := protoregistry.GlobalTypes.FindMessageByName(messageDesc.FullName())
	if err != nil {
		panic(err)
	}
	return messageType.New().Interface()
}

// ObjectHelper contains information about a message type that satisfies the conditions to be considered an object.
type ObjectHelper struct {
	parent     *Helper
	descriptor protoreflect.MessageDescriptor
	template   proto.Message
	list       listInfo
	get        getInfo
	update     updateInfo
	delete     deleteInfo
}

type methodInfo struct {
	path     string
	request  proto.Message
	response proto.Message
}

type getInfo struct {
	methodInfo
	id     protoreflect.FieldDescriptor
	object protoreflect.FieldDescriptor
}

type listInfo struct {
	methodInfo
	filter protoreflect.FieldDescriptor
	items  protoreflect.FieldDescriptor
}

type updateInfo struct {
	methodInfo
	in  protoreflect.FieldDescriptor
	out protoreflect.FieldDescriptor
}

type deleteInfo struct {
	methodInfo
	id protoreflect.FieldDescriptor
}

func (h *ObjectHelper) Descriptor() protoreflect.MessageDescriptor {
	return h.descriptor
}

func (h *ObjectHelper) Instance() proto.Message {
	return proto.Clone(h.template)
}

func (h *ObjectHelper) FullName() protoreflect.FullName {
	return h.descriptor.FullName()
}

func (h *ObjectHelper) String() string {
	return string(h.descriptor.FullName())
}

type ListOptions struct {
	Filter string
}

func (h *ObjectHelper) List(ctx context.Context, options ListOptions) (results []proto.Message, err error) {
	request := proto.Clone(h.list.request)
	if options.Filter != "" {
		request.ProtoReflect().Set(h.list.filter, protoreflect.ValueOfString(options.Filter))
	}
	response := proto.Clone(h.list.response)
	err = h.parent.connection.Invoke(ctx, h.list.path, request, response)
	if err != nil {
		return
	}
	list := response.ProtoReflect().Get(h.list.items).List()
	results = make([]proto.Message, list.Len())
	for i := range list.Len() {
		results[i] = list.Get(i).Message().Interface()
	}
	return
}

func (c *ObjectHelper) Get(ctx context.Context, id string) (result proto.Message, err error) {
	request := proto.Clone(c.get.request)
	c.setId(request, c.get.id, id)
	response := proto.Clone(c.get.response)
	err = c.parent.connection.Invoke(ctx, c.get.path, request, response)
	if err != nil {
		return
	}
	result = c.getObject(response, c.get.object)
	return
}

func (c *ObjectHelper) Update(ctx context.Context, object proto.Message) (result proto.Message, err error) {
	request := proto.Clone(c.update.request)
	c.setObject(request, c.update.in, object)
	response := proto.Clone(c.update.response)
	err = c.parent.connection.Invoke(ctx, c.update.path, request, response)
	if err != nil {
		err = fmt.Errorf("failed to update object: %w", err)
	}
	result = c.getObject(response, c.update.out)
	return
}

func (c *ObjectHelper) setId(message proto.Message, field protoreflect.FieldDescriptor, value string) {
	message.ProtoReflect().Set(field, protoreflect.ValueOfString(value))
}

func (c *ObjectHelper) setObject(message proto.Message, field protoreflect.FieldDescriptor, value proto.Message) {
	message.ProtoReflect().Set(field, protoreflect.ValueOfMessage(value.ProtoReflect()))
}

func (c *ObjectHelper) getObject(message proto.Message, field protoreflect.FieldDescriptor) proto.Message {
	return message.ProtoReflect().Get(field).Message().Interface()
}
