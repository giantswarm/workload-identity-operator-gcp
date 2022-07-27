package serviceaccount_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	serviceaccount "github.com/giantswarm/workload-identity-operator-gcp/controllers"
	"github.com/giantswarm/workload-identity-operator-gcp/webhook"
)

var _ = Describe("Service Account Reconcilation", func() {
	var (
		ctx context.Context

		serviceAccount     *corev1.ServiceAccount
		serviceAccountName = "the-service-account"

		gcpServiceAccount    = "service-account@email"
		workloadIdentityPool = "workload-identity-pool-id"
		identityProvider     = "https://gkehub.googleapis.com/projects/testing-1234/locations/global/memberships/cluster"

		secret     *corev1.Secret
		secretName = fmt.Sprintf("%s-%s", serviceAccountName, serviceaccount.SecretNameSuffix)

		timeout  = time.Second * 5
		interval = time.Millisecond * 250

		secretsIsNotFound = func(secret *corev1.Secret) bool {
			err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      secretName,
			}, secret)

			return err != nil && k8serrors.IsNotFound(err)
		}
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
						webhook.AnnotationGCPServiceAccount:      gcpServiceAccount,
						webhook.AnnotationWorkloadIdentityPoolID: workloadIdentityPool,
						webhook.AnnotationGCPIdentityProvider:    identityProvider,
					},
				},
			}
			Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())
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
	       "file": "/var/run/secrets/tokens/gcp-ksa/token"
	     }
	   }`, workloadIdentityPool, identityProvider, gcpServiceAccount)

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
							webhook.AnnotationGCPServiceAccount:      newGCPServiceAccount,
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
	       "file": "/var/run/secrets/tokens/gcp-ksa/token"
	     }
	   }`, workloadIdentityPool, identityProvider, newGCPServiceAccount)

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

	When("a service account without a workload identity pool is created", func() {
		BeforeEach(func() {
			ctx = context.Background()

			serviceAccount = &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceAccountName,
					Namespace: namespace,
					Annotations: map[string]string{
						webhook.AnnotationGCPServiceAccount:   gcpServiceAccount,
						webhook.AnnotationGCPIdentityProvider: identityProvider,
					},
				},
			}
			Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())
		})

		It("should not create a secret", func() {
			secret = &corev1.Secret{}

			Consistently(secretsIsNotFound(secret)).Should(BeTrue(), "secret is not found")
		})
	})

	When("a service account without a gcpServiceAccount annotation is created", func() {
		BeforeEach(func() {
			ctx = context.Background()

			serviceAccount = &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceAccountName,
					Namespace: namespace,
					Annotations: map[string]string{
						webhook.AnnotationWorkloadIdentityPoolID: workloadIdentityPool,
						webhook.AnnotationGCPIdentityProvider:    identityProvider,
					},
				},
			}
			Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())
		})

		It("should not create a secret", func() {
			secret = &corev1.Secret{}

			Consistently(secretsIsNotFound(secret)).Should(BeTrue(), "secret is not found")
		})
	})

	When("a service account without an identity provider annotation is created", func() {
		BeforeEach(func() {
			ctx = context.Background()

			serviceAccount = &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceAccountName,
					Namespace: namespace,
					Annotations: map[string]string{
						webhook.AnnotationGCPServiceAccount:      gcpServiceAccount,
						webhook.AnnotationWorkloadIdentityPoolID: workloadIdentityPool,
					},
				},
			}
			Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())
		})

		It("should not create a secret", func() {
			secret = &corev1.Secret{}

			Consistently(secretsIsNotFound(secret)).Should(BeTrue(), "secret is not found")
		})
	})
})
