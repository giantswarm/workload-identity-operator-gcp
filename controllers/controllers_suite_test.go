package controllers_test

import (
	"context"
	"fmt"
	"go/build"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	kcapi "k8s.io/client-go/tools/clientcmd/api"
	capg "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	capi "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	"github.com/giantswarm/workload-identity-operator-gcp/tests"
	//+kubebuilder:scaffold:imports
)

func TestK8s(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controllers Suite")
}

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.
var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	namespace string

	cfg    *rest.Config
	scheme = runtime.NewScheme()

	ctx    context.Context
	cancel context.CancelFunc
)

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	tests.GetEnvOrSkip("KUBEBUILDER_ASSETS")

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join(build.Default.GOPATH, "pkg", "mod", "sigs.k8s.io", "cluster-api@v1.1.5", "config", "crd", "bases"),
			filepath.Join(build.Default.GOPATH, "pkg", "mod", "sigs.k8s.io", "cluster-api@v1.1.5", "controlplane", "kubeadm", "config", "crd", "bases"),
			filepath.Join(build.Default.GOPATH, "pkg", "mod", "sigs.k8s.io", "cluster-api-provider-gcp@v1.1.1", "config", "crd", "bases"),
		},
		ErrorIfCRDPathMissing: true,
	}

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(capg.AddToScheme(scheme))
	utilruntime.Must(capi.AddToScheme(scheme))

	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	ctx, cancel = context.WithCancel(context.Background())
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	if testEnv == nil {
		return
	}
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

var _ = BeforeEach(func() {
	namespace = uuid.New().String()
	namespaceObj := &corev1.Namespace{}
	namespaceObj.Name = namespace
	Expect(k8sClient.Create(context.Background(), namespaceObj)).To(Succeed())

	Expect(ensureNamespaceExists(context.Background())).To(Succeed())
})

var _ = AfterEach(func() {
	namespaceObj := &corev1.Namespace{}
	namespaceObj.Name = namespace
	Expect(k8sClient.Delete(context.Background(), namespaceObj)).To(Succeed())
})

func ensureNamespaceExists(ctx context.Context) error {
	namespaceObj := &corev1.Namespace{}

	err := k8sClient.Get(ctx, client.ObjectKey{
		Name: controllers.DefaultMembershipSecretNamespace,
	}, namespaceObj)

	if k8serrors.IsNotFound(err) {
		namespaceObj.Name = controllers.DefaultMembershipSecretNamespace
		err = k8sClient.Create(context.Background(), namespaceObj)

		return err
	}

	return err
}

func KubeConfigFromREST(cfg *rest.Config) ([]byte, error) {
	const envtestName = "envtest"
	kubeConfig := kcapi.NewConfig()
	protocol := "https"
	if !rest.IsConfigTransportTLS(*cfg) {
		protocol = "http"
	}

	// cfg.Host is a URL, so we need to parse it so we can properly append the API path
	baseURL, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("unable to interpret config's host value as a URL: %w", err)
	}

	kubeConfig.Clusters[envtestName] = &kcapi.Cluster{
		Server:                   (&url.URL{Scheme: protocol, Host: baseURL.Host, Path: cfg.APIPath}).String(),
		CertificateAuthorityData: cfg.CAData,
	}
	kubeConfig.AuthInfos[envtestName] = &kcapi.AuthInfo{
		ClientCertificateData: cfg.CertData,
		ClientKeyData:         cfg.KeyData,
		Token:                 cfg.BearerToken,
		Username:              cfg.Username,
		Password:              cfg.Password,
	}
	kcCtx := kcapi.NewContext()
	kcCtx.Cluster = envtestName
	kcCtx.AuthInfo = envtestName
	kubeConfig.Contexts[envtestName] = kcCtx
	kubeConfig.CurrentContext = envtestName

	contents, err := clientcmd.Write(*kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to serialize kubeconfig file: %w", err)
	}
	return contents, nil
}
