package membership

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"google.golang.org/api/googleapi"
	gkehubpb "google.golang.org/genproto/googleapis/cloud/gkehub/v1beta1"

	capg "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
)

const (
	AuthorityIssuer = "https://kubernetes.default.svc.cluster.local"
)

//counterfeiter:generate . GKEMembershipClient
type GKEMembershipClient interface {
	RegisterMembership(ctx context.Context, cluster *capg.GCPCluster, membership *gkehubpb.Membership) error
}

type GKEMembershipReconciler struct {
	client GKEMembershipClient

	Logger logr.Logger
}

func NewGKEClusterReconciler(
	client GKEMembershipClient,
	logger logr.Logger,
) *GKEMembershipReconciler {
	return &GKEMembershipReconciler{
		client: client,
		Logger: logger,
	}
}

func (r *GKEMembershipReconciler) Reconcile(ctx context.Context, gcpCluster *capg.GCPCluster, oidcJwks []byte) (*gkehubpb.Membership, error) {
	logger := r.Logger.WithValues("gkemembership", gcpCluster.Name)

	membership := GenerateMembership(*gcpCluster, oidcJwks)

	err := r.client.RegisterMembership(ctx, gcpCluster, membership)
	if err != nil {
		if hasHttpCode(err, http.StatusConflict) {
			//Expect contents of membership to always be the same for a cluster
			logger.Info(fmt.Sprintf("membership %s already exists", membership.Name))
			return membership, nil
		}

		return membership, err
	}

	return membership, nil
}

func GenerateMembership(cluster capg.GCPCluster, oidcJwks []byte) *gkehubpb.Membership {
	externalId := uuid.New().String()

	membershipId := GenerateMembershipId(cluster)
	name := GenerateMembershipName(cluster)

	workloadIdPool := GenerateWorkpoolId(cluster)
	identityProvider := GenerateIdentityProvider(cluster, membershipId)

	membership := &gkehubpb.Membership{
		Name: name,
		Authority: &gkehubpb.Authority{
			Issuer:               AuthorityIssuer,
			WorkloadIdentityPool: workloadIdPool,
			IdentityProvider:     identityProvider,
			OidcJwks:             oidcJwks,
		},
		ExternalId: externalId,
	}

	return membership
}

func GenerateMembershipId(cluster capg.GCPCluster) string {
	return fmt.Sprintf("%s-%s", cluster.Name, "workload-identity")
}

func GenerateMembershipName(cluster capg.GCPCluster) string {
	return fmt.Sprintf("projects/%s/locations/global/memberships/%s-workload-identity-test", cluster.Spec.Project, cluster.Name)
}

func GenerateWorkpoolId(cluster capg.GCPCluster) string {
	return fmt.Sprintf("%s.svc.id.goog", cluster.Spec.Project)
}

func GenerateIdentityProvider(cluster capg.GCPCluster, membershipId string) string {
	return fmt.Sprintf("https://gkehub.googleapis.com/projects/%s/locations/global/memberships/%s", cluster.Spec.Project, membershipId)
}

func hasHttpCode(err error, statusCode int) bool {
	var googleErr *googleapi.Error
	if errors.As(err, &googleErr) {
		if googleErr.Code == statusCode {
			return true
		}
	}

	return false
}
