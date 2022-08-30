package acceptance_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	infra "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	serviceaccount "github.com/giantswarm/workload-identity-operator-gcp/controllers"
	"github.com/giantswarm/workload-identity-operator-gcp/webhook"
)

var _ = Describe("Workload Identity", func() {
	var (
		ctx context.Context
		pod *corev1.Pod

		clusterName = "acceptance-workload-cluster"
		gcpProject  = "giantswarm-tests"
		gcpCluster  = &infra.GCPCluster{
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
		}
		membershipId   = controllers.GenerateMembershipId(*gcpCluster)
		serviceAccount *corev1.ServiceAccount

		gcpServiceAccount    = "service-account@email"
		workloadIdentityPool = fmt.Sprintf("%s.svc.id.goog", gcpProject)
		identityProvider     = fmt.Sprintf("https://gkehub.googleapis.com/projects/%s/locations/global/memberships/%s", gcpProject, membershipId)

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	BeforeEach(func() {
		ctx = context.Background()

		serviceAccount = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "the-service-account",
				Namespace: namespace,
				Annotations: map[string]string{
					controllers.AnnotationGCPServiceAccount:  gcpServiceAccount,
					webhook.AnnotationWorkloadIdentityPoolID: workloadIdentityPool,
					webhook.AnnotationGCPIdentityProvider:    identityProvider,
				},
			},
		}
		Expect(workloadClient.Create(ctx, serviceAccount)).To(Succeed())

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

		Expect(EnsureClusterCRExists(gcpCluster)).To(Succeed())

		patch := []byte(`{"status":{"ready":true}}`)

		Expect(k8sClient.Status().Patch(ctx, gcpCluster, client.RawPatch(types.MergePatchType, patch))).To(Succeed())
	})

	JustBeforeEach(func() {
		membershipSecret := &corev1.Secret{}

		Eventually(func() error {
			err := workloadClient.Get(ctx, client.ObjectKey{
				Name:      controllers.MembershipSecretName,
				Namespace: controllers.MembershipSecretNamespace,
			}, membershipSecret)

			return err

		}, "120s").Should(Succeed())
	})

	It("Creates the secret with the credentials needed", func() {
		secret := &corev1.Secret{}
		secretName := fmt.Sprintf("%s-%s", serviceAccount.Name, serviceaccount.SecretNameSuffix)

		Eventually(func() error {
			err := workloadClient.Get(ctx, client.ObjectKey{
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
		Expect(workloadClient.Create(ctx, pod)).To(Succeed())

		getPodStatus := func() bool {
			podNamespacedName := types.NamespacedName{
				Name:      "the-pod",
				Namespace: namespace,
			}
			workload := &corev1.Pod{}
			Expect(workloadClient.Get(ctx, podNamespacedName, workload)).To(Succeed())

			if len(workload.Status.ContainerStatuses) == 0 {
				return false
			}

			return workload.Status.ContainerStatuses[0].Ready
		}

		Eventually(getPodStatus, "120s").Should(BeTrue(), "pod container failed to become ready")
		Consistently(getPodStatus, "5s").Should(BeTrue(), "pod container errored")
	})
})

func EnsureClusterCRExists(gcpCluster *infra.GCPCluster) error {
	ctx := context.Background()

	err := k8sClient.Get(ctx, client.ObjectKey{
		Name:      gcpCluster.Name,
		Namespace: gcpCluster.Namespace,
	}, gcpCluster)

	if k8serrors.IsNotFound(err) {
		err = k8sClient.Create(context.Background(), gcpCluster)
		if k8serrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}

	return err
}
