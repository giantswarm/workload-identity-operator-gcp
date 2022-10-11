package acceptance_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	"github.com/giantswarm/workload-identity-operator-gcp/tests"
	"github.com/giantswarm/workload-identity-operator-gcp/webhook"
)

var _ = Describe("Workload Identity", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	var (
		ctx context.Context

		pod            *corev1.Pod
		serviceAccount *corev1.ServiceAccount

		membershipId         string
		gcpServiceAccount    string
		workloadIdentityPool string
		identityProvider     string
	)

	BeforeEach(func() {
		ctx = context.Background()

		gcpServiceAccount = "service-account@email"
		workloadIdentityPool = fmt.Sprintf("%s.svc.id.goog", gcpProject)
		membershipId = "testing-123"
		identityProvider = fmt.Sprintf("https://gkehub.googleapis.com/projects/%s/locations/global/memberships/%s", gcpProject, membershipId)

		tests.EnsureMembershipSecretExists(k8sClient, workloadIdentityPool, identityProvider)

		serviceAccount = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "the-service-account",
				Namespace: namespace,
				Annotations: map[string]string{
					controllers.AnnotationGCPServiceAccount: gcpServiceAccount,
				},
			},
		}
		Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())

		pod = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "the-pod",
				Namespace: namespace,
				Labels: map[string]string{
					webhook.LabelWorkloadIdentity: "enabled",
				},
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: "the-service-account",
				Containers: []corev1.Container{
					{
						Name:    "first-container",
						Image:   "alpine:latest",
						Command: []string{"sh"},
						Args: []string{
							"-c",
							`if ! [[ -f $GOOGLE_APPLICATION_CREDENTIALS ]]; then
                  echo "credentials missing";
                  cat $GOOGLE_APPLICATION_CREDENTIALS;
                  exit 1;
               fi;
               echo "sleeping...";
               sleep 3600;`,
						},
					},
				},
			},
		}
	})

	JustBeforeEach(func() {
		membershipSecret := &corev1.Secret{}

		Eventually(func() error {
			err := k8sClient.Get(ctx, client.ObjectKey{
				Name:      controllers.MembershipSecretName,
				Namespace: controllers.DefaultMembershipSecretNamespace,
			}, membershipSecret)

			return err
		}, "120s").Should(Succeed())
	})

	It("Creates the secret with the credentials needed", func() {
		secret := &corev1.Secret{}
		secretName := fmt.Sprintf("%s-%s", serviceAccount.Name, controllers.SecretNameSuffix)

		Eventually(func() error {
			err := k8sClient.Get(ctx, client.ObjectKey{
				Namespace: namespace,
				Name:      secretName,
			}, secret)

			return err
		}, timeout, interval).Should(BeNil())

		Expect(secret).ToNot(BeNil())
		Expect(secret.Name).To(Equal(secretName))
		Expect(secret.Namespace).To(Equal(namespace))

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

	It("Injects the credentials file in the pod", func() {
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())

		getPodStatus := func() bool {
			podNamespacedName := types.NamespacedName{
				Name:      "the-pod",
				Namespace: namespace,
			}
			workload := &corev1.Pod{}
			Expect(k8sClient.Get(ctx, podNamespacedName, workload)).To(Succeed())

			if len(workload.Status.ContainerStatuses) == 0 {
				return false
			}

			return workload.Status.ContainerStatuses[0].Ready
		}

		Eventually(getPodStatus, "120s").Should(BeTrue(), "pod container failed to become ready")
		Consistently(getPodStatus, "5s").Should(BeTrue(), "pod container errored")
	})
})
