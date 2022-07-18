package acceptance_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/giantswarm/workload-identity-operator-gcp/webhook"
)

var _ = Describe("Workload Identity", func() {
	var (
		ctx context.Context
		pod *corev1.Pod
	)

	BeforeEach(func() {
		ctx = context.Background()
		serviceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "the-service-account",
				Namespace: namespace,
				Annotations: map[string]string{
					webhook.AnnotationGCPServiceAccount:      "service-account@email",
					webhook.AnnotationWorkloadIdentityPoolID: "workload-identity-pool-id",
					webhook.AnnotationGCPIdentityProvider:    "gcp-identity-provider",
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

		Eventually(getPodStatus, "30s").Should(BeTrue(), "pod container failed to become ready")
		Consistently(getPodStatus, "5s").Should(BeTrue(), "pod container errored")
	})
})
