package acceptance_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/workload-identity-operator-gcp/serviceaccount"
	"github.com/giantswarm/workload-identity-operator-gcp/tests"
)

var (
	k8sClient client.Client

	namespace    string
	namespaceObj *corev1.Namespace

	ctx    context.Context
	cancel context.CancelFunc
)

func TestAcceptance(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Acceptance Suite")
}

var _ = BeforeSuite(func() {
	tests.GetEnvOrSkip("KUBECONFIG")

	config, err := controllerruntime.GetConfig()
	Expect(err).NotTo(HaveOccurred())

	mgr, err := ctrl.NewManager(config, ctrl.Options{})
	Expect(err).NotTo(HaveOccurred(), "failed to create manager")

	serviceAccountReconciler := &serviceaccount.ServiceAccountReconciler{
		Client: mgr.GetClient(),
		Logger: ctrl.Log.WithName("service-account-reconciler"),
		Scheme: mgr.GetScheme(),
	}

	err = serviceAccountReconciler.SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel = context.WithCancel(context.TODO())
	k8sClient, err = client.New(config, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	go func() {
		err := mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred(), "failed to start manager")
	}()
})

var _ = BeforeEach(func() {
	namespace = uuid.New().String()
	namespaceObj = &corev1.Namespace{}
	namespaceObj.Name = namespace
	Expect(k8sClient.Create(context.Background(), namespaceObj)).To(Succeed())
})

var _ = AfterEach(func() {
	Expect(k8sClient.Delete(context.Background(), namespaceObj)).To(Succeed())
})

var _ = AfterSuite(func() {
	cancel()
})
