/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"time"

	gkehub "cloud.google.com/go/gkehub/apiv1beta1"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	gax "github.com/googleapis/gax-go"
	gkehubpb "google.golang.org/genproto/googleapis/cloud/gkehub/v1beta1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/giantswarm/workload-identity-operator-gcp/webhook"

	infra "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
)

const (
	AnnotationWorkloadIdentityEnabled  = "giantswarm.io/workload-identity-enabled"
	AnnoationMembershipSecretCreatedBy = "app.kubernetes.io/created-by"
	SuffixMembershipName               = "workload-identity-test"
	MembershipSecretName               = "workload-identity-operator-gcp-membership"
	MembershipSecretNamespace          = "giantswarm"
	KeyWorkloadClusterConfig           = "value"

	AuthorityIssuer = "https://kubernetes.default.svc.cluster.local"
)

type GKEMembershipClient interface {
	CreateMembership(context context.Context, req *gkehubpb.CreateMembershipRequest, opts ...gax.CallOption) (*gkehub.CreateMembershipOperation, error)
	GetMembership(ctx context.Context, req *gkehubpb.GetMembershipRequest, opts ...gax.CallOption) (*gkehubpb.Membership, error)
}

// GCPClusterReconciler reconciles a GCPCluster object
type GCPClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Logger logr.Logger

	GKEHubMembershipClient GKEMembershipClient
}

//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=gcpclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=gcpclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=gcpclusters/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the GCPCluster object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *GCPClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Logger.WithValues("gcpcluster", req.NamespacedName)

	gcpCluster := &infra.GCPCluster{}

	err := r.Get(ctx, req.NamespacedName, gcpCluster)
	if err != nil {
		logger.Error(err, "could not get gcp cluster")
		return reconcile.Result{}, nil
	}

	if !r.hasWorkloadIdentityEnabled(gcpCluster) {
		message := fmt.Sprintf("skipping Cluster %s because workload identity is not enabled", gcpCluster.Name)
		logger.Info(message)
		return reconcile.Result{}, nil
	}

	if !gcpCluster.Status.Ready {
		message := fmt.Sprintf("skipping Cluster %s because its not yet ready", gcpCluster.Name)
		logger.Info(message)
		return reconcile.Result{}, nil
	}

	config, err := r.getWorkloadClusterConfig(ctx, gcpCluster, req.Namespace)
	if err != nil {
		logger.Error(err, "failed to get kubeconfig")
		return reconcile.Result{}, err
	}

	workloadClusterClient, err := client.New(config, client.Options{})
	if err != nil {
		logger.Error(err, "failed to create workload cluster client")
		return reconcile.Result{}, err
	}

	nodes := &corev1.NodeList{}
	err = workloadClusterClient.List(ctx, nodes)
	if err != nil {
		logger.Error(err, "failed to list nodes in cluster")
		return reconcile.Result{}, err
	}

	if !hasOneNodeReady(nodes) {
		message := fmt.Sprintf("Skipping cluster %s because no node is ready", req.NamespacedName)
		logger.Info(message)
		return reconcile.Result{}, err
	}

	logger.Info(fmt.Sprintf("Cluster name is %s", gcpCluster.Name))

	oidcJwks, err := r.getOIDCJWKS(config)
	if err != nil {
		logger.Error(err, "failed to get cluster oidc jwks")
		return reconcile.Result{}, err
	}

	membership := GenerateMembership(*gcpCluster, oidcJwks)
	membershipExists, err := r.doesMembershipExist(ctx, membership.Name)
	if err != nil {
		logger.Error(err, "failed to check memberships existence")
		return reconcile.Result{}, err
	}

	if !membershipExists {
		err = r.registerMembership(ctx, gcpCluster, membership)
		if err != nil {
			logger.Error(err, "failed to register cluster membership")
			return reconcile.Result{}, err
		}

		logger.Info(fmt.Sprintf("membership %s created", membership.Name))
	}

	membershipJson, err := json.Marshal(membership)
	if err != nil {
		logger.Error(err, "failed to marshal membership json")
		return reconcile.Result{}, err
	}

	secret := r.generateMembershipSecret(membershipJson, gcpCluster)
	err = workloadClusterClient.Create(ctx, secret)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		logger.Error(err, "failed to create secret on workload cluster")
		return reconcile.Result{}, err
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: time.Minute * 5,
	}, nil
}

func (r *GCPClusterReconciler) hasWorkloadIdentityEnabled(cluster *infra.GCPCluster) bool {
	_, exists := cluster.Annotations[AnnotationWorkloadIdentityEnabled]
	return exists
}

func (r *GCPClusterReconciler) doesMembershipExist(ctx context.Context, name string) (bool, error) {
	req := &gkehubpb.GetMembershipRequest{
		Name: name,
	}

	_, err := r.GKEHubMembershipClient.GetMembership(ctx, req)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return false, nil
		}

		r.Logger.Error(err, "error occurred while checking memberships existence")
		return false, err
	}

	return true, nil
}

func (r *GCPClusterReconciler) getWorkloadClusterConfig(ctx context.Context, cluster *infra.GCPCluster, namespace string) (*rest.Config, error) {
	secret := &corev1.Secret{}
	secretName := fmt.Sprintf("%s-kubeconfig", cluster.Name)

	err := r.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: namespace,
	}, secret)

	if err != nil {
		r.Logger.Error(err, "could not get cluster secret")
		return &rest.Config{}, err
	}

	data := secret.Data[KeyWorkloadClusterConfig]

	config, err := clientcmd.NewClientConfigFromBytes(data)
	if err != nil {
		return &rest.Config{}, err
	}

	return config.ClientConfig()
}

func (r *GCPClusterReconciler) getOIDCJWKS(config *rest.Config) ([]byte, error) {
	reqUrl := fmt.Sprintf("%s/openid/v1/jwks", config.Host)

	httpClient, err := rest.HTTPClientFor(config)
	if err != nil {
		r.Logger.Error(err, "failed to create http client")
		return []byte{}, err
	}

	resp, err := httpClient.Get(reqUrl)
	if err != nil {
		r.Logger.Error(err, "failed to fetch jwks")
		return []byte{}, err
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		r.Logger.Error(err, "failed to read oidc jwks response body")
		return []byte{}, err
	}

	return body, nil
}

func GenerateMembership(cluster infra.GCPCluster, oidcJwks []byte) *gkehubpb.Membership {
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

func (r *GCPClusterReconciler) registerMembership(ctx context.Context, cluster *infra.GCPCluster, membership *gkehubpb.Membership) error {
	project := cluster.Spec.Project
	membershipId := GenerateMembershipId(*cluster)
	parent := fmt.Sprintf("projects/%s/locations/global", project)

	req := &gkehubpb.CreateMembershipRequest{
		Parent:       parent,
		MembershipId: membershipId,
		Resource:     membership,
	}

	op, err := r.GKEHubMembershipClient.CreateMembership(ctx, req)
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

func (r *GCPClusterReconciler) generateMembershipSecret(membershipJson []byte, cluster *infra.GCPCluster) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      MembershipSecretName,
			Namespace: MembershipSecretNamespace,
			Annotations: map[string]string{
				AnnoationMembershipSecretCreatedBy: cluster.Name,
				AnnotationSecretManagedBy:          SecretManagedBy,
			},
		},
		StringData: map[string]string{
			webhook.SecretKeyGoogleApplicationCredentials: string(membershipJson),
		},
	}

	finalizer := GenerateMembershipSecretFinalizer(SecretManagedBy)
	ok := controllerutil.AddFinalizer(secret, finalizer)
	if !ok {
		r.Logger.Info("failed to add finalizer")
	}

	return secret
}

func hasOneNodeReady(nodes *corev1.NodeList) bool {
	for _, node := range nodes.Items {
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				return true
			}

		}
	}

	return false
}

func GenerateMembershipId(cluster infra.GCPCluster) string {
	return fmt.Sprintf("%s-%s", cluster.Name, SuffixMembershipName)
}

func GenerateMembershipName(cluster infra.GCPCluster) string {
	return fmt.Sprintf("projects/%s/locations/global/memberships/%s-workload-identity-test", cluster.Spec.Project, cluster.Name)
}

func GenerateWorkpoolId(cluster infra.GCPCluster) string {
	return fmt.Sprintf("%s.svc.id.goog", cluster.Spec.Project)
}

func GenerateIdentityProvider(cluster infra.GCPCluster, membershipId string) string {
	return fmt.Sprintf("https://gkehub.googleapis.com/projects/%s/locations/global/memberships/%s", cluster.Spec.Project, membershipId)
}

func GenerateMembershipSecretFinalizer(value string) string {
	return fmt.Sprintf("%s/finalizer", value)
}

// SetupWithManager sets up the controller with the Manager.
func (r *GCPClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		For(&infra.GCPCluster{}).
		Complete(r)
}
