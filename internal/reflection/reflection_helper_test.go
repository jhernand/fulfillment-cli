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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	ffv1 "github.com/innabox/fulfillment-cli/internal/api/fulfillment/v1"
	"github.com/innabox/fulfillment-cli/internal/testing"
	. "github.com/innabox/fulfillment-cli/internal/testing"
)

var _ = Describe("Reflection helper", func() {
	var (
		ctx        context.Context
		server     *Server
		connection *grpc.ClientConn
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Create the server:
		server = NewServer()
		DeferCleanup(server.Stop)

		// Create the client connection:
		connection, err = grpc.NewClient(
			server.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)
	})

	Describe("Creation", func() {
		It("Can be created with all the mandatory parameters", func() {
			helper, err := NewHelper().
				SetLogger(logger).
				SetConnection(connection).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(helper).ToNot(BeNil())
		})

		It("Can't be created without a logger", func() {
			helper, err := NewHelper().
				SetConnection(connection).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(helper).To(BeNil())
		})

		It("Can't be created without a connection", func() {
			helper, err := NewHelper().
				SetLogger(logger).
				Build()
			Expect(err).To(MatchError("gRPC connection is mandatory"))
			Expect(helper).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var helper *Helper

		BeforeEach(func() {
			var err error
			helper, err = NewHelper().
				SetLogger(logger).
				SetConnection(connection).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Returns object types in singular", func() {
			Expect(helper.Singulars()).To(ConsistOf(
				"cluster",
				"clusterorder",
				"clustertemplate",
				"hostclass",
			))
		})

		It("Returns object types in plural", func() {
			Expect(helper.Plurals()).To(ConsistOf(
				"clusters",
				"clusterorders",
				"clustertemplates",
				"hostclasses",
			))
		})

		DescribeTable(
			"Lookup by object type",
			func(objectType string, expectedFullName string) {
				objectHelper := helper.Lookup(objectType)
				Expect(objectHelper).ToNot(BeNil())
				Expect(string(objectHelper.FullName())).To(Equal(expectedFullName))
			},
			Entry(
				"Cluster in singular",
				"cluster",
				"fulfillment.v1.Cluster",
			),
			Entry(
				"Cluster in plural",
				"clusters",
				"fulfillment.v1.Cluster",
			),
			Entry(
				"Cluster in singular upper case",
				"CLUSTER",
				"fulfillment.v1.Cluster",
			),
			Entry(
				"Cluster order in plural camel case",
				"ClusterOrders",
				"fulfillment.v1.ClusterOrder",
			),
			Entry(
				"Host class in plural",
				"hostclasses",
				"fulfillment.v1.HostClass",
			),
		)

		DescribeTable(
			"Returns descriptor",
			func(objectType string, expectedFullName string) {
				objectHelper := helper.Lookup(objectType)
				Expect(objectHelper).ToNot(BeNil())
				objectDescriptor := objectHelper.Descriptor()
				Expect(objectDescriptor).ToNot(BeNil())
				Expect(string(objectDescriptor.FullName())).To(Equal(expectedFullName))
			},
			Entry(
				"Cluster",
				"cluster",
				"fulfillment.v1.Cluster",
			),
			Entry(
				"Cluster template",
				"clustertemplate",
				"fulfillment.v1.ClusterTemplate",
			),
			Entry(
				"Cluster order",
				"clusterorder",
				"fulfillment.v1.ClusterOrder",
			),
			Entry(
				"Host class",
				"hostclass",
				"fulfillment.v1.HostClass",
			),
		)

		DescribeTable(
			"Creates instance",
			func(objectType string, expectedInstance proto.Message) {
				objectHelper := helper.Lookup(objectType)
				Expect(objectHelper).ToNot(BeNil())
				actualInstance := objectHelper.Instance()
				Expect(proto.Equal(actualInstance, expectedInstance)).To(BeTrue())
			},
			Entry(
				"Cluster",
				"cluster",
				&ffv1.Cluster{},
			),
			Entry(
				"Cluster template",
				"clustertemplate",
				&ffv1.ClusterTemplate{},
			),
			Entry(
				"Cluster order",
				"clusterorder",
				&ffv1.ClusterOrder{},
			),
			Entry(
				"Host class",
				"hostclass",
				&ffv1.HostClass{},
			),
		)

		It("Invokes get method", func() {
			// Register a clusters server that responds to the get request:
			ffv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
				GetFunc: func(ctx context.Context, request *ffv1.ClustersGetRequest,
				) (response *ffv1.ClustersGetResponse, err error) {
					defer GinkgoRecover()
					Expect(request.GetId()).To(Equal("123"))
					response = ffv1.ClustersGetResponse_builder{
						Object: ffv1.Cluster_builder{
							Id: "123",
							Status: ffv1.ClusterStatus_builder{
								State: ffv1.ClusterState_CLUSTER_STATE_READY,
							}.Build(),
						}.Build(),
					}.Build()
					return
				},
			})

			// Start the server:
			server.Start()

			// Use the helper to send the request, and verify the response:
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			object, err := objectHelper.Get(ctx, "123")
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(object, ffv1.Cluster_builder{
				Id: "123",
				Status: ffv1.ClusterStatus_builder{
					State: ffv1.ClusterState_CLUSTER_STATE_READY,
				}.Build(),
			}.Build())).To(BeTrue())
		})

		It("Invokes list method", func() {
			// Register a clusters server that responds to the list request:
			ffv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
				ListFunc: func(ctx context.Context, request *ffv1.ClustersListRequest,
				) (response *ffv1.ClustersListResponse, err error) {
					response = ffv1.ClustersListResponse_builder{
						Size:  proto.Int32(2),
						Total: proto.Int32(2),
						Items: []*ffv1.Cluster{
							ffv1.Cluster_builder{
								Id: "123",
							}.Build(),
							ffv1.Cluster_builder{
								Id: "456",
							}.Build(),
						},
					}.Build()
					return
				},
			})

			// Start the server:
			server.Start()

			// Use the helper to send the request, and verify the response:
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			items, err := objectHelper.List(ctx, ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(items).To(HaveLen(2))
			Expect(proto.Equal(
				items[0],
				ffv1.Cluster_builder{
					Id: "123",
				}.Build(),
			)).To(BeTrue())
			Expect(proto.Equal(
				items[1],
				ffv1.Cluster_builder{
					Id: "456",
				}.Build(),
			)).To(BeTrue())
		})

		It("Invokes update method", func() {
			// Register a clusters server that responds to the update request:
			ffv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
				UpdateFunc: func(ctx context.Context, request *ffv1.ClustersUpdateRequest,
				) (response *ffv1.ClustersUpdateResponse, err error) {
					defer GinkgoRecover()
					Expect(proto.Equal(
						request.Object,
						ffv1.Cluster_builder{
							Id: "123",
							Spec: ffv1.ClusterSpec_builder{
								NodeSets: map[string]*ffv1.ClusterNodeSet{
									"xyz": ffv1.ClusterNodeSet_builder{
										Size: 3,
									}.Build(),
								},
							}.Build(),
						}.Build(),
					)).To(BeTrue())
					response = ffv1.ClustersUpdateResponse_builder{
						Object: ffv1.Cluster_builder{
							Id: "123",
							Spec: ffv1.ClusterSpec_builder{
								NodeSets: map[string]*ffv1.ClusterNodeSet{
									"xyz": ffv1.ClusterNodeSet_builder{
										HostClass: "acme_1tib",
										Size:      3,
									}.Build(),
								},
							}.Build(),
						}.Build(),
					}.Build()
					return
				},
			})

			// Start the server:
			server.Start()

			// Use the helper to send the request, and verify the response:
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			object, err := objectHelper.Update(ctx, ffv1.Cluster_builder{
				Id: "123",
				Spec: ffv1.ClusterSpec_builder{
					NodeSets: map[string]*ffv1.ClusterNodeSet{
						"xyz": ffv1.ClusterNodeSet_builder{
							Size: 3,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(
				object,
				ffv1.Cluster_builder{
					Id: "123",
					Spec: ffv1.ClusterSpec_builder{
						NodeSets: map[string]*ffv1.ClusterNodeSet{
							"xyz": ffv1.ClusterNodeSet_builder{
								HostClass: "acme_1tib",
								Size:      3,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			)).To(BeTrue())
		})
	})
})
