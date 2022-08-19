package controllers_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	gkehub "cloud.google.com/go/gkehub/apiv1beta1"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/api/option"
	gkehubpb "google.golang.org/genproto/googleapis/cloud/gkehub/v1beta1"
	"google.golang.org/genproto/googleapis/longrunning"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	kcapi "k8s.io/client-go/tools/clientcmd/api"
	infra "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
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

type FakeGKEServer struct {
	gkehubpb.UnimplementedGkeHubMembershipServiceServer
}

func (fs *FakeGKEServer) CreateMembership(context.Context, *gkehubpb.CreateMembershipRequest) (*longrunning.Operation, error) {
	op := &longrunning.Operation{
		Name: "create",
		Done: true,
		Result: &longrunning.Operation_Response{
			Response: &anypb.Any{
				TypeUrl: "http://google.cloud.gkehub.v1beta1.Membership",
				Value:   []byte{},
			},
		},
	}
	return op, nil
}

func (fs *FakeGKEServer) GetMembership(ctx context.Context, req *gkehubpb.GetMembershipRequest) (*gkehubpb.Membership, error) {
	err := status.Error(codes.NotFound, fmt.Sprintf("%s not found", req.Name))

	return &gkehubpb.Membership{}, err
}

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

	cfg    *rest.Config
	scheme = runtime.NewScheme()

	ctx    context.Context
	cancel context.CancelFunc

	gkeClient *gkehub.GkeHubMembershipClient
)

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	tests.GetEnvOrSkip("KUBEBUILDER_ASSETS")

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "tests", "crds")},
		ErrorIfCRDPathMissing: true,
	}

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(infra.AddToScheme(scheme))

	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	randomNumber, err := getRandomPort()
	Expect(err).NotTo(HaveOccurred(), "failed to generate random port number")

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	metricsPort := fmt.Sprintf(":%s", randomNumber)

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:             scheme,
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
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	fakeServer := &FakeGKEServer{}
	l, err := net.Listen("tcp", "localhost:0")
	Expect(err).To(BeNil())

	gsrv := grpc.NewServer()
	gkehubpb.RegisterGkeHubMembershipServiceServer(gsrv, fakeServer)
	fakeServerAddr := l.Addr().String()
	go func() {
		if err := gsrv.Serve(l); err != nil {
			Expect(err).NotTo(HaveOccurred(), "failed to start grpc server")
		}
	}()

	// Create a client.
	gkeClient, err = gkehub.NewGkeHubMembershipClient(ctx,
		option.WithEndpoint(fakeServerAddr),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithInsecure()),
	)

	Expect(err).NotTo(HaveOccurred())

	clusterReconciler := controllers.GCPClusterReconciler{
		Client:                 k8sClient,
		Scheme:                 mgr.GetScheme(),
		Logger:                 logf.Log,
		GKEHubMembershipClient: gkeClient,
	}

	err = clusterReconciler.SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

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
