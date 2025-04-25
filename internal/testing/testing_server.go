/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package testing

import (
	"context"
	"net"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/grpc"

	ffv1 "github.com/innabox/fulfillment-cli/internal/api/fulfillment/v1"
)

// Server is a gRPC server used only for tests.
type Server struct {
	listener net.Listener
	server   *grpc.Server
}

// NewServer creates a new gRPC server that listens in a randomly selected port in the local host.
func NewServer() *Server {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	server := grpc.NewServer()
	return &Server{
		listener: listener,
		server:   server,
	}
}

// Adress returns the address where the server is listening.
func (s *Server) Address() string {
	return s.listener.Addr().String()
}

// Registrar returns the registrar that can be used to register server implementations.
func (s *Server) Registrar() grpc.ServiceRegistrar {
	return s.server
}

// Start starts the server. This needs to be done after registering all server implementations, and before trying to
// call any of them.
func (s *Server) Start() {
	go func() {
		defer GinkgoRecover()
		err := s.server.Serve(s.listener)
		Expect(err).ToNot(HaveOccurred())
	}()
}

// Stop stops the server, closing all connections and releasing all the resources it was using.
func (s *Server) Stop() {
	s.server.Stop()
}

// Make sure that we implement the interface.
var _ ffv1.ClustersServer = (*ClustersServerFuncs)(nil)

// ClustersServerFuncs is an implementation of the clusters server that uses configurable functions to implement the
// methods.
type ClustersServerFuncs struct {
	ffv1.UnimplementedClustersServer

	CreateFunc               func(context.Context, *ffv1.ClustersCreateRequest) (*ffv1.ClustersCreateResponse, error)
	DeleteFunc               func(context.Context, *ffv1.ClustersDeleteRequest) (*ffv1.ClustersDeleteResponse, error)
	GetFunc                  func(context.Context, *ffv1.ClustersGetRequest) (*ffv1.ClustersGetResponse, error)
	ListFunc                 func(context.Context, *ffv1.ClustersListRequest) (*ffv1.ClustersListResponse, error)
	GetKubeconfigFunc        func(context.Context, *ffv1.ClustersGetKubeconfigRequest) (*ffv1.ClustersGetKubeconfigResponse, error)
	GetKubeconfigViaHttpFunc func(context.Context, *ffv1.ClustersGetKubeconfigViaHttpRequest) (*httpbody.HttpBody, error)
	UpdateFunc               func(context.Context, *ffv1.ClustersUpdateRequest) (*ffv1.ClustersUpdateResponse, error)
}

func (s *ClustersServerFuncs) Create(ctx context.Context,
	request *ffv1.ClustersCreateRequest) (response *ffv1.ClustersCreateResponse, err error) {
	response, err = s.CreateFunc(ctx, request)
	return
}

func (s *ClustersServerFuncs) Delete(ctx context.Context,
	request *ffv1.ClustersDeleteRequest) (response *ffv1.ClustersDeleteResponse, err error) {
	response, err = s.DeleteFunc(ctx, request)
	return
}

func (s *ClustersServerFuncs) Get(ctx context.Context,
	request *ffv1.ClustersGetRequest) (response *ffv1.ClustersGetResponse, err error) {
	response, err = s.GetFunc(ctx, request)
	return
}

func (s *ClustersServerFuncs) GetKubeconfig(ctx context.Context,
	request *ffv1.ClustersGetKubeconfigRequest) (response *ffv1.ClustersGetKubeconfigResponse, err error) {
	response, err = s.GetKubeconfigFunc(ctx, request)
	return
}

func (s *ClustersServerFuncs) GetKubeconfigViaHttp(ctx context.Context,
	request *ffv1.ClustersGetKubeconfigViaHttpRequest) (response *httpbody.HttpBody, err error) {
	response, err = s.GetKubeconfigViaHttpFunc(ctx, request)
	return
}

func (s *ClustersServerFuncs) List(ctx context.Context,
	request *ffv1.ClustersListRequest) (response *ffv1.ClustersListResponse, err error) {
	response, err = s.ListFunc(ctx, request)
	return
}

func (s *ClustersServerFuncs) Update(ctx context.Context,
	request *ffv1.ClustersUpdateRequest) (response *ffv1.ClustersUpdateResponse, err error) {
	response, err = s.UpdateFunc(ctx, request)
	return
}
