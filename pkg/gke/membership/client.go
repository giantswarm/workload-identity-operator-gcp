package membership

import (
	"context"
	"fmt"

	gkehub "cloud.google.com/go/gkehub/apiv1beta1"
	gkehubpb "google.golang.org/genproto/googleapis/cloud/gkehub/v1beta1"
	capg "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type Client struct {
	gkeClient *gkehub.GkeHubMembershipClient
}

func NewClient(client *gkehub.GkeHubMembershipClient) *Client {
	return &Client{
		gkeClient: client,
	}
}

func (c *Client) RegisterMembership(ctx context.Context, cluster *capg.GCPCluster, membership *gkehubpb.Membership) error {
	logger := log.FromContext(ctx)
	logger = logger.WithName("gke-client")

	project := cluster.Spec.Project
	membershipId := GenerateMembershipId(*cluster)
	parent := fmt.Sprintf("projects/%s/locations/global", project)

	req := &gkehubpb.CreateMembershipRequest{
		Parent:       parent,
		MembershipId: membershipId,
		Resource:     membership,
	}

	op, err := c.gkeClient.CreateMembership(ctx, req)
	if err != nil {
		logger.Error(err, "failed to create membership operation")
		return err
	}

	_, err = op.Wait(ctx)

	return err
}
