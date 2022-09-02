package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	infra "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	serviceaccount "github.com/giantswarm/workload-identity-operator-gcp/controllers"
	"github.com/giantswarm/workload-identity-operator-gcp/webhook"
)

var _ = Describe("Service Account Reconcilation", func() {
	var (
		ctx context.Context

		clusterName = "krillin"
		gcpProject  = "testing-1234"
		gcpCluster  = &infra.GCPCluster{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterName,
			},
			Spec: infra.GCPClusterSpec{
				Project: gcpProject,
			},
		}
		membershipId = controllers.GenerateMembershipId(*gcpCluster)

		serviceAccount     *corev1.ServiceAccount
		serviceAccountName = "the-service-account"

		gcpServiceAccount    = "service-account@email"
		workloadIdentityPool = controllers.GenerateWorkpoolId(*gcpCluster)
		identityProvider     = controllers.GenerateIdentityProvider(*gcpCluster, membershipId)

		secret     *corev1.Secret
		secretName = fmt.Sprintf("%s-%s", serviceAccountName, serviceaccount.SecretNameSuffix)

		timeout  = time.Second * 5
		interval = time.Millisecond * 250

	)

	SetDefaultConsistentlyDuration(timeout)
	SetDefaultConsistentlyPollingInterval(interval)
	SetDefaultEventuallyPollingInterval(interval)
	SetDefaultEventuallyTimeout(timeout)

	When("a correctly annotated service account is created", func() {
		BeforeEach(func() {
			ctx = context.Background()

			serviceAccount = &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceAccountName,
					Namespace: namespace,
					Annotations: map[string]string{
						controllers.AnnotationGCPServiceAccount:  gcpServiceAccount,
						webhook.AnnotationWorkloadIdentityPoolID: workloadIdentityPool,
						webhook.AnnotationGCPIdentityProvider:    identityProvider,
					},
				},
			}
			Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())

			Expect(ensureMembershipSecretExists(gcpCluster)).To(Succeed())
		})

		JustBeforeEach(func() {
			secret = &corev1.Secret{}

			Eventually(func() error {
				err := k8sClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      secretName,
				}, secret)

				return err
			}).Should(Succeed())
		})

		It("should create a secret with the correct credentials", func() {
			Expect(secret).ToNot(BeNil())
			Expect(secret.Name).To(Equal(secretName))
			Expect(secret.Namespace).To(Equal(namespace))
			Expect(secret.OwnerReferences).ToNot(BeEmpty())
			Expect(secret.OwnerReferences).Should(ContainElement(HaveField("Name", serviceAccountName)))

			data := string(secret.Data["config"])

			expectedData := fmt.Sprintf(`{
                 "type": "external_account",
                 "audience": "identitynamespace:%[1]s:%[2]s",
                 "service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/%[3]s:generateAccessToken",
                 "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
                 "token_url": "https://sts.googleapis.com/v1/token",
                 "credential_source": {
                   "file": "%[4]s/%[5]s"
                 }
               }`, workloadIdentityPool, identityProvider, gcpServiceAccount,
				controllers.VolumeMountWorkloadIdentityPath, controllers.ServiceAccountTokenPath)

			Expect(data).Should(MatchJSON(expectedData))
		})

		When("the service account is updated", func() {
			const newGCPServiceAccount string = "gcp-service-account@gcp.co"

			BeforeEach(func() {
				serviceAccount = &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      serviceAccountName,
						Namespace: namespace,
						Annotations: map[string]string{
							controllers.AnnotationGCPServiceAccount:  newGCPServiceAccount,
							webhook.AnnotationWorkloadIdentityPoolID: workloadIdentityPool,
							webhook.AnnotationGCPIdentityProvider:    identityProvider,
						},
					},
				}
				Expect(k8sClient.Update(ctx, serviceAccount)).To(Succeed())
			})

			It("should update the secret", func() {
				expectedData := fmt.Sprintf(`{
                     "type": "external_account",
                     "audience": "identitynamespace:%[1]s:%[2]s",
                     "service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/%[3]s:generateAccessToken",
                     "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
                     "token_url": "https://sts.googleapis.com/v1/token",
                     "credential_source": {
                       "file": "%[4]s/%[5]s"
                     }
                   }`, workloadIdentityPool, identityProvider, newGCPServiceAccount,
					controllers.VolumeMountWorkloadIdentityPath, controllers.ServiceAccountTokenPath)

				secret = &corev1.Secret{}

				Eventually(func() string {
					_ = k8sClient.Get(ctx, client.ObjectKey{
						Namespace: namespace,
						Name:      secretName,
					}, secret)

					Expect(secret).ToNot(BeNil())
					Expect(secret.Name).To(Equal(secretName))
					Expect(secret.Namespace).To(Equal(namespace))
					Expect(secret.OwnerReferences).ToNot(BeEmpty())
					Expect(secret.OwnerReferences).Should(ContainElement(HaveField("Name", serviceAccountName)))

					data := string(secret.Data["config"])

					return data
				}).Should(MatchJSON(expectedData))
			})
		})
	})

})

func ensureMembershipSecretExists(gcpCluster *infra.GCPCluster) error {
	membershipSecret := &corev1.Secret{}

	err := k8sClient.Get(ctx, client.ObjectKey{
		Name:      controllers.MembershipSecretName,
		Namespace: controllers.MembershipSecretNamespace,
	}, membershipSecret)

	if k8serrors.IsNotFound(err) {
		oidcJwks := []byte{}

		membership := controllers.GenerateMembership(*gcpCluster, oidcJwks)
		membershipJson, err := json.Marshal(membership)

		Expect(err).To(BeNil())

		membershipSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      controllers.MembershipSecretName,
				Namespace: controllers.MembershipSecretNamespace,
				Annotations: map[string]string{
					controllers.AnnoationMembershipSecretCreatedBy: gcpCluster.Name,
					controllers.AnnotationSecretManagedBy:          controllers.SecretManagedBy,
				},
			},
			StringData: map[string]string{
				controllers.SecretKeyGoogleApplicationCredentials: string(membershipJson),
			},
		}
		err = k8sClient.Create(context.Background(), membershipSecret)

		return err
	}

	return err
}
