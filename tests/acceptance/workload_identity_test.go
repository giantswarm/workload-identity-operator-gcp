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

	serviceaccount "github.com/giantswarm/workload-identity-operator-gcp/controllers"
	"github.com/giantswarm/workload-identity-operator-gcp/webhook"
)

var _ = Describe("Workload Identity", func() {
	var (
		ctx context.Context
		pod *corev1.Pod

		serviceAccount *corev1.ServiceAccount

		gcpServiceAccount    = "service-account@email"
		workloadIdentityPool = "workload-identity-pool-id"
		identityProvider     = "https://gkehub.googleapis.com/projects/testing-1234/locations/global/memberships/cluster"

		timeout  = time.Second * 5
		interval = time.Millisecond * 250
	)

	BeforeEach(func() {
		ctx = context.Background()
		serviceAccount = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "the-service-account",
				Namespace: namespace,
				Annotations: map[string]string{
					webhook.AnnotationGCPServiceAccount:      gcpServiceAccount,
					webhook.AnnotationWorkloadIdentityPoolID: workloadIdentityPool,
					webhook.AnnotationGCPIdentityProvider:    identityProvider,
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

	It("Creates the secret with the credentials needed", func() {
		secret := &corev1.Secret{}
		secretName := fmt.Sprintf("%s-%s", serviceAccount.Name, serviceaccount.SecretNameSuffix)

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
	       "file": "/var/run/secrets/tokens/gcp-ksa/token"
	     }
	   }`, workloadIdentityPool, identityProvider, gcpServiceAccount)

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

		Eventually(getPodStatus, "60s").Should(BeTrue(), "pod container failed to become ready")
		Consistently(getPodStatus, "5s").Should(BeTrue(), "pod container errored")
	})
})
