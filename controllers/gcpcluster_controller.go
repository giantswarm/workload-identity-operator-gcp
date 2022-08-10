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

	gkehub "cloud.google.com/go/gkehub/apiv1beta1"
	"github.com/giantswarm/workload-identity-operator-gcp/webhook"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	gkehubpb "google.golang.org/genproto/googleapis/cloud/gkehub/v1beta1"
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

	infra "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
)

const (
	AnnotationWorkloadIdentityEnabled  = "giantswarm.io/workload-identity-enabled"
	AnnoationMembershipSecretCreatedBy = "app.kubernetes.io/created-by"
	SuffixMembershipName               = "workload-identity-test"
)

type MembershipSecret struct {
	Name      string
	Namespace string
	Data      string
	ManagedBy string
	Metadata  string
}

// GCPClusterReconciler reconciles a GCPCluster object
type GCPClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Logger logr.Logger
}

//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io.my.domain,resources=gcpclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io.my.domain,resources=gcpclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io.my.domain,resources=gcpclusters/finalizers,verbs=update

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

	cl, err := client.New(config, client.Options{})
	if err != nil {
		logger.Error(err, "failed to create client")
		return reconcile.Result{}, err
	}

	nodes := &corev1.NodeList{}
	err = cl.List(ctx, nodes)
	if err != nil {
		logger.Error(err, "failed to list nodes in cluster")
		return reconcile.Result{}, err
	}

	hasAReadyNode := hasOneNodeReady(nodes)
	if !hasAReadyNode {
		message := fmt.Sprintf("Skipping cluster %s because no node is ready", req.NamespacedName)
		logger.Info(message)
		return reconcile.Result{}, err
	}

	logger.Info(fmt.Sprintf("Cluster name is %s", gcpCluster.Name))

	oidcJwks, err := r.getOIDCJWKS(config)
	if err != nil {
		logger.Error(err, "failed to get oidc jwks")
		return reconcile.Result{}, err
	}

	membershipId := GenerateMembershipId(gcpCluster)
	membership := r.generateMembership(gcpCluster, oidcJwks)
	membershipExists, err := r.doesMembershipExist(ctx, membershipId)

	if err != nil {
		logger.Error(err, "failed to check memberships existence")
		return reconcile.Result{}, err
	}

	if !membershipExists {
		err = r.registerMembership(ctx, gcpCluster, membership)
		if err != nil {
			logger.Error(err, "failed to register cluster")
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
	err = cl.Create(ctx, secret)
	if err != nil {
		logger.Error(err, "failed to create secret on workload cluster")
	}

	return ctrl.Result{}, nil
}

func (r *GCPClusterReconciler) hasWorkloadIdentityEnabled(cluster *infra.GCPCluster) bool {
	_, exists := cluster.Annotations[AnnotationWorkloadIdentityEnabled]
	return exists
}

func (r *GCPClusterReconciler) doesMembershipExist(ctx context.Context, name string) (bool, error) {
	c, err := gkehub.NewGkeHubMembershipClient(ctx)
	if err != nil {
		r.Logger.Error(err, "failed to create gke hub membership client")
		return false, err
	}
	defer c.Close()

	req := &gkehubpb.GetMembershipRequest{
		Name: name,
	}

	_, err = c.GetMembership(ctx, req)
	if err != nil && !k8serrors.IsNotFound(err) {
		r.Logger.Error(err, "error occurred while checking memberships existence")
		return false, err
	}

	if k8serrors.IsNotFound(err) {
		return false, nil
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
		r.Logger.Error(err, "Could not get cluster secret")
		return &rest.Config{}, err
	}

	data := secret.Data["value"]

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
		r.Logger.Error(err, "failed to read request body")
		return []byte{}, err
	}

	return body, nil

}

func (r *GCPClusterReconciler) generateMembership(cluster *infra.GCPCluster, oidcJwks []byte) *gkehubpb.Membership {
	externalId := uuid.New().String()
	project := cluster.Spec.Project

	name := fmt.Sprintf("projects/%s/locations/global/memberships/%s-workload-identity-test", project, cluster.Name)

	workloadIdPool := fmt.Sprintf("%s.svc.id.goog", project)
	identityProvider := fmt.Sprintf("https://gkehub.googleapis.com/projects/%s/locations/global/memberships/%s", project, name)
	issuer := "https://kubernetes.default.svc.cluster.local"

	membership := &gkehubpb.Membership{
		Name: name,
		Authority: &gkehubpb.Authority{
			Issuer:               issuer,
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
	membershipId := GenerateMembershipId(cluster)
	parent := fmt.Sprintf("projects/%s/locations/global", project)

	c, err := gkehub.NewGkeHubMembershipClient(ctx)
	if err != nil {
		r.Logger.Error(err, "failed to create gke hub membership client")
		return err
	}
	defer c.Close()

	req := &gkehubpb.CreateMembershipRequest{
		Parent:       parent,
		MembershipId: membershipId,
		Resource:     membership,
	}

	op, err := c.CreateMembership(ctx, req)
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
			Name:      GenerateMembershipId(cluster),
			Namespace: "giantswarm",
			Annotations: map[string]string{
				AnnoationMembershipSecretCreatedBy: cluster.Name,
				AnnotationSecretManagedBy:          SecretManagedBy,
			},
		},
		StringData: map[string]string{
			webhook.SecretKeyGoogleApplicationCredentials: string(membershipJson),
		},
	}

	finalizer := fmt.Sprintf("%s/finalizer", SecretManagedBy)
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

func GenerateMembershipId(cluster *infra.GCPCluster) string {
	return fmt.Sprintf("%s-%s", cluster.Name, SuffixMembershipName)
}

// SetupWithManager sets up the controller with the Manager.
func (r *GCPClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		For(&infra.GCPCluster{}).
		Complete(r)
}
