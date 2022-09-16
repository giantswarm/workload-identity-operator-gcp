package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/api/googleapi"
	gkehubpb "google.golang.org/genproto/googleapis/cloud/gkehub/v1beta1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	capg "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	capi "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	gke "github.com/giantswarm/workload-identity-operator-gcp/pkg/gke/membership"
	"github.com/giantswarm/workload-identity-operator-gcp/pkg/gke/membership/membershipfakes"
	"github.com/giantswarm/workload-identity-operator-gcp/tests"
)

var _ = Describe("GCPCluster Reconcilation", func() {
	var (
		ctx context.Context

		fakeGKEClient     *membershipfakes.FakeGKEMembershipClient
		clusterReconciler *controllers.GCPClusterReconciler

		clusterName = "krillin"
		gcpProject  = "testing-1234"

		gcpCluster          *capg.GCPCluster
		kubeadmControlPlane *capi.KubeadmControlPlane

		timeout  = time.Second * 5
		interval = time.Millisecond * 250

		result      reconcile.Result
		reconcilErr error
	)

	SetDefaultConsistentlyDuration(timeout)
	SetDefaultConsistentlyPollingInterval(interval)
	SetDefaultEventuallyPollingInterval(interval)
	SetDefaultEventuallyTimeout(timeout)

	When("a GCP cluster is created", func() {
		BeforeEach(func() {
			ctx = context.Background()

			gcpCluster = &capg.GCPCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
					Annotations: map[string]string{
						controllers.AnnotationWorkloadIdentityEnabled: "true",
					},
				},
				Spec: capg.GCPClusterSpec{
					Project: gcpProject,
				},
				Status: capg.GCPClusterStatus{
					Ready: true,
				},
			}

			kubeadmControlPlane = &capi.KubeadmControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
			}
			Expect(k8sClient.Create(ctx, kubeadmControlPlane)).To(Succeed())

			controlPlaneStatus := capi.KubeadmControlPlaneStatus{
				Ready: true,
			}

			tests.PatchControlPlaneStatus(k8sClient, kubeadmControlPlane, controlPlaneStatus)

			Expect(k8sClient.Create(ctx, gcpCluster)).To(Succeed())
			clusterStatus := capg.GCPClusterStatus{
				Ready: true,
			}
			tests.PatchClusterStatus(k8sClient, gcpCluster, clusterStatus)

			secretName := fmt.Sprintf("%s-kubeconfig", gcpCluster.Name)
			kubeconfig, err := KubeConfigFromREST(cfg)

			Expect(err).To(BeNil())

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				Data: map[string][]byte{
					"value": kubeconfig,
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			fakeGKEClient = new(membershipfakes.FakeGKEMembershipClient)

			gkeMembershipReconciler := gke.NewGKEClusterReconciler(
				fakeGKEClient,
				ctrl.Log.WithName("gke-membership-reconciler"),
			)

			clusterReconciler = &controllers.GCPClusterReconciler{
				Client:                  k8sClient,
				Logger:                  logf.Log,
				GKEMembershipReconciler: gkeMembershipReconciler,
			}
		})

		JustBeforeEach(func() {
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      gcpCluster.Name,
					Namespace: gcpCluster.Namespace,
				},
			}
			result, reconcilErr = clusterReconciler.Reconcile(ctx, req)
		})

		AfterEach(func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      controllers.MembershipSecretName,
					Namespace: controllers.MembershipSecretNamespace,
				},
			}
			err := k8sClient.Delete(ctx, secret)
			if !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("reconciles successfully", func() {
			Expect(reconcilErr).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
		})

		It("creates a gke membership secret with the correct credentials", func() {
			secret := &corev1.Secret{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Namespace: controllers.MembershipSecretNamespace,
				Name:      controllers.MembershipSecretName,
			}, secret)
			Expect(err).NotTo(HaveOccurred())

			Expect(secret).ToNot(BeNil())
			Expect(secret.Name).To(Equal(controllers.MembershipSecretName))
			Expect(secret.Namespace).To(Equal(controllers.MembershipSecretNamespace))
			Expect(secret.Annotations).Should(HaveKeyWithValue(controllers.AnnoationMembershipSecretCreatedBy, clusterName))
			Expect(secret.Annotations).Should(HaveKeyWithValue(controllers.AnnotationSecretManagedBy, controllers.SecretManagedBy))
			Expect(controllerutil.ContainsFinalizer(secret, controllers.GenerateMembershipSecretFinalizer(controllers.SecretManagedBy)))

			data := secret.Data[controllers.SecretKeyGoogleApplicationCredentials]

			var membership gkehubpb.Membership
			membershipId := gke.GenerateMembershipId(*gcpCluster)
			Expect(json.Unmarshal(data, &membership)).To(Succeed())

			Expect(membership.Name).To(Equal(gke.GenerateMembershipName(*gcpCluster)))
			Expect(membership.Authority.Issuer).To(Equal(gke.AuthorityIssuer))
			Expect(membership.Authority.WorkloadIdentityPool).To(Equal(gke.GenerateWorkpoolId(*gcpCluster)))
			Expect(membership.Authority.IdentityProvider).To(Equal(gke.GenerateIdentityProvider(*gcpCluster, membershipId)))
			Expect(MatchRegexp(`[a-zA-Z0-9][a-zA-Z0-9_\-\.]*`).Match(membership.ExternalId)).To(BeTrue())
		})

		When("the kubeadm control plane is not ready", func() {
			BeforeEach(func() {
				controlPlaneStatus := capi.KubeadmControlPlaneStatus{
					Ready: false,
				}

				tests.PatchControlPlaneStatus(k8sClient, kubeadmControlPlane, controlPlaneStatus)
			})

			It("requeues the request", func() {
				Expect(reconcilErr).NotTo(HaveOccurred())
				Expect(result.Requeue).To(BeTrue())
				Expect(result.RequeueAfter).To(Equal(time.Second * 15))
			})
		})

		When("the workload cluster config is missing", func() {
			BeforeEach(func() {
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("%s-kubeconfig", gcpCluster.Name),
						Namespace: namespace,
					},
				}

				Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
			})

			It("returns a not found error", func() {
				Expect(reconcilErr).To(HaveOccurred())
				Expect(k8serrors.IsNotFound(reconcilErr)).To(BeTrue())
			})
		})

		When("the workload cluster config is broken", func() {
			BeforeEach(func() {
				secret := &corev1.Secret{}

				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      fmt.Sprintf("%s-kubeconfig", gcpCluster.Name),
					Namespace: namespace,
				}, secret)

				Expect(err).ToNot(HaveOccurred())

				secret.Data = map[string][]byte{
					"value": []byte("{'title': 'Its a cold cold world'}"),
				}

				Expect(k8sClient.Update(ctx, secret)).To(Succeed())
			})

			It("returns an error", func() {
				Expect(reconcilErr).To(HaveOccurred())
				Expect(clientcmd.IsConfigurationInvalid(reconcilErr)).To(BeTrue())
			})
		})

		When("the membership client fails", func() {
			BeforeEach(func() {
				oops := errors.New("something went wrong")
				fakeGKEClient.RegisterMembershipReturns(oops)
			})

			It("should return an error", func() {
				Expect(reconcilErr).To(HaveOccurred())
			})

			It("should not create a membership secret", func() {
				secret := &corev1.Secret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      controllers.MembershipSecretName,
					Namespace: controllers.MembershipSecretNamespace,
				}, secret)

				Expect(err).To(HaveOccurred())
				Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			})
		})

		When("the membership is already registered", func() {
			BeforeEach(func() {
				responseBody := ioutil.NopCloser(bytes.NewReader([]byte(`{"value":"Already Exists"}`)))
				resp := &http.Response{
					StatusCode: 409,
					Body:       responseBody,
				}

				oops := googleapi.CheckResponse(resp)

				fakeGKEClient.RegisterMembershipReturns(oops)
			})

			It("should not return an error", func() {
				Expect(reconcilErr).NotTo(HaveOccurred())
			})

			It("should create a membership secret", func() {
				secret := &corev1.Secret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      controllers.MembershipSecretName,
					Namespace: controllers.MembershipSecretNamespace,
				}, secret)

				Expect(err).ToNot(HaveOccurred())
			})
		})

		When("workload identity is not enabled", func() {
			BeforeEach(func() {
				cluster := gcpCluster.DeepCopy()
				cluster.Annotations = map[string]string{}

				Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

			})

			It("should return an error and skip cluster", func() {
				Expect(reconcilErr).ToNot(HaveOccurred())
			})

			It("should not create a membership secret", func() {
				secret := &corev1.Secret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      controllers.MembershipSecretName,
					Namespace: controllers.MembershipSecretNamespace,
				}, secret)

				Expect(err).To(HaveOccurred())
				Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			})
		})

	})
})
