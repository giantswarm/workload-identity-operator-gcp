package acceptance_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infra "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"

	"github.com/giantswarm/workload-identity-operator-gcp/tests"
)

var (
	k8sClient client.Client

	workloadClient client.Client

	namespace    string
	namespaceObj *corev1.Namespace
)

func TestAcceptance(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Acceptance Suite")
}

var _ = BeforeSuite(func() {
	tests.GetEnvOrSkip("KUBECONFIG")

	scheme := runtime.NewScheme()

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(infra.AddToScheme(scheme))

	config, err := controllerruntime.GetConfig()
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(config, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	home := tests.GetEnvOrSkip("HOME")
	workloadClusterConfigPath := fmt.Sprintf("%s/.kube/workload-cluster.yaml", home)

	wcfg := clientcmd.GetConfigFromFileOrDie(workloadClusterConfigPath)
	wcdcfg := clientcmd.NewDefaultClientConfig(*wcfg, nil)
	wccfg, err := wcdcfg.ClientConfig()
	Expect(err).NotTo(HaveOccurred())

	workloadClient, err = client.New(wccfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

})

var _ = BeforeEach(func() {
	namespace = uuid.New().String()
	namespaceObj = &corev1.Namespace{}
	namespaceObj.Name = namespace

	Expect(workloadClient.Create(context.Background(), namespaceObj)).To(Succeed())
})

var _ = AfterEach(func() {
	Expect(workloadClient.Delete(context.Background(), namespaceObj)).To(Succeed())
})
