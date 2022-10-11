package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	capg "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	capi "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/fleet-membership-operator-gcp/types"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
)

func GetEnvOrSkip(env string) string {
	value := os.Getenv(env)
	if value == "" {
		ginkgo.Skip(fmt.Sprintf("%s not exported", env))
	}

	return value
}

func PatchClusterStatus(k8sClient client.Client, cluster *capg.GCPCluster, status capg.GCPClusterStatus) {
	patchedCluster := cluster.DeepCopy()
	patchedCluster.Status = status
	err := k8sClient.Status().Patch(context.Background(), patchedCluster, client.MergeFrom(cluster))
	Expect(err).NotTo(HaveOccurred())

	nsName := k8stypes.NamespacedName{
		Name:      cluster.Name,
		Namespace: cluster.Namespace,
	}
	Expect(k8sClient.Get(context.Background(), nsName, cluster)).To(Succeed())
}

func PatchControlPlaneStatus(k8sClient client.Client, controlplane *capi.KubeadmControlPlane, status capi.KubeadmControlPlaneStatus) {
	patched := controlplane.DeepCopy()
	patched.Status = status
	err := k8sClient.Status().Patch(context.Background(), patched, client.MergeFrom(controlplane))
	Expect(err).NotTo(HaveOccurred())

	nsName := k8stypes.NamespacedName{
		Name:      controlplane.Name,
		Namespace: controlplane.Namespace,
	}
	Expect(k8sClient.Get(context.Background(), nsName, controlplane)).To(Succeed())
}

func EnsureMembershipSecretExists(k8sClient client.Client, workloadIdentityPool, identityProvider string) {
	membership := types.MembershipData{
		WorkloadIdentityPool: workloadIdentityPool,
		IdentityProvider:     identityProvider,
	}
	membershipJson, err := json.Marshal(membership)
	Expect(err).To(Succeed())

	membershipSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      controllers.MembershipSecretName,
			Namespace: controllers.DefaultMembershipSecretNamespace,
		},
		StringData: map[string]string{
			controllers.SecretKeyGoogleApplicationCredentials: string(membershipJson),
		},
	}
	err = k8sClient.Create(context.Background(), membershipSecret)
	if !k8serrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}
