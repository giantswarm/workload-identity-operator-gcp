package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cloud.google.com/go/gkehub/apiv1beta1/gkehubpb"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	capg "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	serviceaccount "github.com/giantswarm/workload-identity-operator-gcp/controllers"
	"github.com/giantswarm/workload-identity-operator-gcp/webhook"
)

var _ = Describe("Service Account Reconcilation", func() {
	var (
		ctx context.Context

		timeout  = time.Second * 5
		interval = time.Millisecond * 250

		clusterName          string
		gcpProject           string
		serviceAccountName   string
		gcpServiceAccount    string
		secretName           string
		workloadIdentityPool string
		identityProvider     string

		gcpCluster     *capg.GCPCluster
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
			clusterName = "krillin"
			gcpProject = "testing-1234"
			serviceAccountName = "the-service-account"
			gcpServiceAccount = "service-account@email"

			secretName = fmt.Sprintf("%s-%s", serviceAccountName, serviceaccount.SecretNameSuffix)
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

			gcpCluster = &capg.GCPCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name: clusterName,
				},
				Spec: capg.GCPClusterSpec{
					Project: gcpProject,
				},
			}
			createMembershipSecret(gcpCluster)

			workloadIdentityPool = "test.svc.id.goog"
			identityProvider = "https://test.default.local"

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

func createMembershipSecret(gcpCluster *capg.GCPCluster) {
	oidcJwks := []byte{}

	membership := GenerateMembership(oidcJwks)
	membershipJson, err := json.Marshal(membership)

	Expect(err).NotTo(HaveOccurred())

	membershipSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      controllers.MembershipSecretName,
			Namespace: controllers.DefaultMembershipSecretNamespace,
			Annotations: map[string]string{
				"app.kubernetes.io/created-by":        gcpCluster.Name,
				controllers.AnnotationSecretManagedBy: controllers.SecretManagedBy,
			},
		},
		StringData: map[string]string{
			controllers.SecretKeyGoogleApplicationCredentials: string(membershipJson),
		},
	}
	err = k8sClient.Create(context.Background(), membershipSecret)
	Expect(err).NotTo(HaveOccurred())
}

func GenerateMembership(oidcJwks []byte) *gkehubpb.Membership {
	externalId := uuid.New().String()

	name := "testing-membership"
	workloadIdPool := "test.svc.id.goog"
	identityProvider := "https://test.default.local"
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
