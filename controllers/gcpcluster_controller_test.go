package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gkehubpb "google.golang.org/genproto/googleapis/cloud/gkehub/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	infra "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	gke "github.com/giantswarm/workload-identity-operator-gcp/pkg/gke/membership"
)

var _ = Describe("GCPCluster Reconcilation", func() {
	var (
		ctx context.Context

		clusterName = "krillin"
		gcpProject  = "testing-1234"

		gcpCluster = &infra.GCPCluster{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: "giantswarm",
				Annotations: map[string]string{
					controllers.AnnotationWorkloadIdentityEnabled: "true",
				},
			},
			Spec: infra.GCPClusterSpec{
				Project: gcpProject,
			},
			Status: infra.GCPClusterStatus{
				Ready: true,
			},
		}

		secret     *corev1.Secret
		secretName = controllers.MembershipSecretName

		timeout  = time.Second * 5
		interval = time.Millisecond * 250
	)

	SetDefaultConsistentlyDuration(timeout)
	SetDefaultConsistentlyPollingInterval(interval)
	SetDefaultEventuallyPollingInterval(interval)
	SetDefaultEventuallyTimeout(timeout)

	When("a GCP cluster is created", func() {
		BeforeEach(func() {
			ctx = context.Background()

			Expect(k8sClient.Create(ctx, gcpCluster)).To(Succeed())

			secretName := fmt.Sprintf("%s-kubeconfig", gcpCluster.Name)
			contents, err := KubeConfigFromREST(cfg)

			Expect(err).To(BeNil())

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: "giantswarm",
				},
				Data: map[string][]byte{
					"value": contents,
				},
			}

			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: "giantswarm",
				},
				Spec: corev1.NodeSpec{},
			}

			err = k8sClient.Create(ctx, node)
			Expect(err).To(BeNil())

			err = k8sClient.Get(context.Background(), client.ObjectKey{
				Namespace: "giantswarm",
				Name:      clusterName,
			}, node)

			nodePatch := []byte(`{"status": {"conditions":[{"type": "nodeReady", "status": "true"}]}}`)
			Expect(k8sClient.Status().Patch(ctx, node, client.RawPatch(types.MergePatchType, nodePatch))).To(Succeed())

			Expect(err).To(BeNil())
			patch := []byte(`{"status":{"ready":true}}`)
			Expect(k8sClient.Status().Patch(ctx, gcpCluster, client.RawPatch(types.MergePatchType, patch))).To(Succeed())
		})

		JustBeforeEach(func() {
			secret = &corev1.Secret{}

			Eventually(func() error {
				err := k8sClient.Get(ctx, client.ObjectKey{
					Namespace: controllers.MembershipSecretNamespace,
					Name:      controllers.MembershipSecretName,
				}, secret)

				return err

			}).Should(Succeed())
		})

		It("should create a gke membership secret with the correct credentials", func() {
			Expect(secret).ToNot(BeNil())
			Expect(secret.Name).To(Equal(secretName))
			Expect(secret.Namespace).To(Equal(controllers.MembershipSecretNamespace))
			Expect(secret.Annotations).Should(HaveKeyWithValue(controllers.AnnoationMembershipSecretCreatedBy, clusterName))
			Expect(secret.Annotations).Should(HaveKeyWithValue(controllers.AnnotationSecretManagedBy, controllers.SecretManagedBy))
			Expect(controllerutil.ContainsFinalizer(secret, controllers.GenerateMembershipSecretFinalizer(controllers.SecretManagedBy)))

			data := secret.Data[controllers.SecretKeyGoogleApplicationCredentials]

			var membership gkehubpb.Membership
			membershipId := gke.GenerateMembershipId(*gcpCluster)
			Expect(json.Unmarshal(data, &membership)).To(Succeed())

			Expect(membership.Name).To(Equal(gke.GenerateMembershipName(*gcpCluster)))
			Expect(membership.Authority.Issuer).To(Equal(gke.AuthorityIssuer))
			Expect(membership.Authority.WorkloadIdentityPool).To(Equal(gke.GenerateWorkpoolId(*gcpCluster)))
			Expect(membership.Authority.IdentityProvider).To(Equal(gke.GenerateIdentityProvider(*gcpCluster, membershipId)))
			Expect(MatchRegexp(`[a-zA-Z0-9][a-zA-Z0-9_\-\.]*`).Match(membership.ExternalId)).To(BeTrue())
		})

	})
})
