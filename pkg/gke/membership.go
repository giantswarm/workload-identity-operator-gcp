package gke

import (
	"context"
	"fmt"

	gkehub "cloud.google.com/go/gkehub/apiv1beta1"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/googleapis/gax-go"
	gkehubpb "google.golang.org/genproto/googleapis/cloud/gkehub/v1beta1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	capg "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
)

const (
	AuthorityIssuer = "https://kubernetes.default.svc.cluster.local"
)

type GKEMembershipClient interface {
	CreateMembership(context context.Context, req *gkehubpb.CreateMembershipRequest, opts ...gax.CallOption) (*gkehub.CreateMembershipOperation, error)
	GetMembership(ctx context.Context, req *gkehubpb.GetMembershipRequest, opts ...gax.CallOption) (*gkehubpb.Membership, error)
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
	membershipExists, err := r.doesMembershipExist(ctx, membership.Name)
	if err != nil {
		logger.Error(err, "failed to check memberships existence")
		return membership, err
	}

	if !membershipExists {
		err = r.registerMembership(ctx, gcpCluster, membership)
		if err != nil {
			logger.Error(err, "failed to register cluster membership")
			return membership, err
		}

		logger.Info(fmt.Sprintf("membership %s created", membership.Name))
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

func (r *GKEMembershipReconciler) doesMembershipExist(ctx context.Context, name string) (bool, error) {
	req := &gkehubpb.GetMembershipRequest{
		Name: name,
	}

	_, err := r.client.GetMembership(ctx, req)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return false, nil
		}

		message := fmt.Sprintf("error occurred while checking membership - %+v", req)
		r.Logger.Error(err, message)
		return false, err
	}

	return true, nil
}

func (r *GKEMembershipReconciler) registerMembership(ctx context.Context, cluster *capg.GCPCluster, membership *gkehubpb.Membership) error {
	project := cluster.Spec.Project
	membershipId := GenerateMembershipId(*cluster)
	parent := fmt.Sprintf("projects/%s/locations/global", project)

	req := &gkehubpb.CreateMembershipRequest{
		Parent:       parent,
		MembershipId: membershipId,
		Resource:     membership,
	}

	op, err := r.client.CreateMembership(ctx, req)
	if err != nil {
		r.Logger.Error(err, "failed to create membership operation")
		return err
	}

	_, err = op.Wait(ctx)
	if err != nil {
		r.Logger.Error(err, "failed whilst waiting for create membership operation to compelete")
		return err
	}

	return nil
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

func GenerateMembershipSecretFinalizer(value string) string {
	return fmt.Sprintf("%s/finalizer", value)
}
