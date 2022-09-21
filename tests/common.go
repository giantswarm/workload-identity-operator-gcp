package tests

import (
	"context"
	"fmt"
	"os"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	capg "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	capi "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

	nsName := types.NamespacedName{
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

	nsName := types.NamespacedName{
		Name:      controlplane.Name,
		Namespace: controlplane.Namespace,
	}
	Expect(k8sClient.Get(context.Background(), nsName, controlplane)).To(Succeed())
}
