package controllers_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	"github.com/giantswarm/workload-identity-operator-gcp/tests"
)

var _ = Describe("Service Account Reconcilation", func() {
	var (
		ctx context.Context

		timeout  = time.Second * 5
		interval = time.Millisecond * 250

		serviceAccountName   string
		gcpServiceAccount    string
		secretName           string
		workloadIdentityPool string
		identityProvider     string

		serviceAccount *corev1.ServiceAccount

		reconciler *controllers.ServiceAccountReconciler

		result      reconcile.Result
		reconcilErr error
	)

	SetDefaultConsistentlyDuration(timeout)
	SetDefaultConsistentlyPollingInterval(interval)
	SetDefaultEventuallyPollingInterval(interval)
	SetDefaultEventuallyTimeout(timeout)

	When("a correctly annotated service account is created", func() {
		BeforeEach(func() {
			ctx = context.Background()
			serviceAccountName = "the-service-account"
			gcpServiceAccount = "service-account@email"

			secretName = fmt.Sprintf("%s-%s", serviceAccountName, controllers.SecretNameSuffix)
			serviceAccount = &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceAccountName,
					Namespace: namespace,
					Annotations: map[string]string{
						controllers.AnnotationGCPServiceAccount: gcpServiceAccount,
					},
				},
			}
			Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())

			workloadIdentityPool = "test.svc.id.goog"
			identityProvider = "https://test.default.local"
			tests.EnsureMembershipSecretExists(k8sClient, workloadIdentityPool, identityProvider)

			reconciler = &controllers.ServiceAccountReconciler{
				Client: k8sClient,
				Logger: ctrl.Log.WithName("service-account-reconciler"),
				Scheme: scheme,
			}
		})

		JustBeforeEach(func() {
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      serviceAccount.Name,
					Namespace: serviceAccount.Namespace,
				},
			}
			result, reconcilErr = reconciler.Reconcile(ctx, req)
		})

		AfterEach(func() {
			membershipSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      controllers.MembershipSecretName,
					Namespace: controllers.DefaultMembershipSecretNamespace,
				},
			}
			Expect(k8sClient.Delete(ctx, membershipSecret)).To(Succeed())
		})

		It("reconciles successfuly", func() {
			Expect(reconcilErr).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
		})

		It("should create a secret with the correct credentials", func() {
			secret := &corev1.Secret{}
			err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      secretName,
			}, secret)
			Expect(err).NotTo(HaveOccurred())

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
							controllers.AnnotationGCPServiceAccount: newGCPServiceAccount,
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

				secret := &corev1.Secret{}
				err := k8sClient.Get(ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      secretName,
				}, secret)
				Expect(err).NotTo(HaveOccurred())

				Expect(secret).ToNot(BeNil())
				Expect(secret.Name).To(Equal(secretName))
				Expect(secret.Namespace).To(Equal(namespace))
				Expect(secret.OwnerReferences).ToNot(BeEmpty())
				Expect(secret.OwnerReferences).Should(ContainElement(HaveField("Name", serviceAccountName)))

				data := string(secret.Data["config"])
				Expect(data).To(MatchJSON(expectedData))
			})
		})
	})
})
