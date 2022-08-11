package controllers_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strconv"
	"testing"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	serviceaccount "github.com/giantswarm/workload-identity-operator-gcp/controllers"
	"github.com/giantswarm/workload-identity-operator-gcp/tests"
	//+kubebuilder:scaffold:imports
)

func TestK8s(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "K8s Suite")
}

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.
var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	namespace string

	ctx    context.Context
	cancel context.CancelFunc
)

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	tests.GetEnvOrSkip("KUBEBUILDER_ASSETS")

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	//+kubebuilder:scaffold:scheme

	randomNumber, err := getRandomPort()
	Expect(err).NotTo(HaveOccurred(), "failed to generate random port number")

	metricsPort := fmt.Sprintf(":%s", randomNumber)

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		MetricsBindAddress: metricsPort,
	})
	Expect(err).NotTo(HaveOccurred(), "failed to create manager")

	serviceAccountReconciler := &serviceaccount.ServiceAccountReconciler{
		Client: mgr.GetClient(),
		Logger: ctrl.Log.WithName("service-account-reconciler"),
		Scheme: mgr.GetScheme(),
	}

	err = serviceAccountReconciler.SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel = context.WithCancel(context.TODO())
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	go func() {
		err := mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred(), "failed to start manager")
	}()
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
		Name: controllers.MembershipSecretNamespace,
	}, namespaceObj)

	if k8serrors.IsNotFound(err) {
		namespaceObj.Name = controllers.MembershipSecretNamespace
		err = k8sClient.Create(context.Background(), namespaceObj)

		return err
	}

	return err
}

func getRandomPort() (string, error) {
	var min int64 = 30000
	var max int64 = 33000

	randomNumber, err := rand.Int(rand.Reader, big.NewInt(max-min))

	if err != nil {
		return "", err
	}
	port := randomNumber.Int64() + min
	portString := strconv.FormatInt(port, 10)

	return portString, nil
}
