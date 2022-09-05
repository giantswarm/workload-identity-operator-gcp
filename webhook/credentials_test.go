package webhook_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gomodules.xyz/jsonpatch/v2"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	infra "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	"github.com/giantswarm/workload-identity-operator-gcp/pkg/gke"
	"github.com/giantswarm/workload-identity-operator-gcp/webhook"
)

var _ = Describe("Credentials", func() {
	var (
		ctx                context.Context
		credentialsWebhook *webhook.CredentialsInjector

		pod            corev1.Pod
		serviceAccount *corev1.ServiceAccount
		gcpCluster     *infra.GCPCluster
		request        admission.Request
		response       admission.Response

		gcpProject           = "testing-1234"
		workloadIdentityPool = fmt.Sprintf("%s.svc.id.goog", gcpProject)
	)

	BeforeEach(func() {
		ctx = context.Background()

		decoder, err := admission.NewDecoder(runtime.NewScheme())
		Expect(err).NotTo(HaveOccurred())
		credentialsWebhook = webhook.NewCredentialsInjector(k8sClient, decoder)

		serviceAccount = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "the-service-account",
				Namespace: namespace,
				Annotations: map[string]string{
					controllers.AnnotationGCPServiceAccount: "service-account@email",
				},
			},
		}
		Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())

		pod = corev1.Pod{
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
						Name: "first-container",
						Env: []corev1.EnvVar{
							{Name: "FOO", Value: "BAR"},
							{Name: "BAR", Value: "BAZ"},
						},
					},
					{
						Name: "second-container",
						Env: []corev1.EnvVar{
							{Name: "BOO", Value: "FAR"},
							{Name: "FAR", Value: "BOO"},
						},
					},
				},
			},
		}

		clusterName := "krillin"
		gcpCluster = &infra.GCPCluster{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterName,
			},
			Spec: infra.GCPClusterSpec{
				Project: gcpProject,
			},
		}

		request = admission.Request{
			AdmissionRequest: admissionv1.AdmissionRequest{
				Object:    encodeObject(pod),
				Operation: admissionv1.Create,
				Namespace: namespace,
			},
		}
	})

	JustBeforeEach(func() {
		Expect(ensureMembershipSecretExists(gcpCluster)).To(Succeed())
		response = credentialsWebhook.Handle(ctx, request)
	})

	It("injects the env var in all containers of the pod", func() {
		Expect(response.AdmissionResponse.Allowed).To(BeTrue())
		Expect(response.Patches).To(ContainElements(
			jsonpatch.Operation{
				Operation: "add",
				Path:      "/spec/containers/0/env/2",
				Value: map[string]interface{}{
					"name":  webhook.EnvKeyGoogleApplicationCredentials,
					"value": "/var/run/secrets/workload-identity/google-application-credentials.json",
				},
			},
			jsonpatch.Operation{
				Operation: "add",
				Path:      "/spec/containers/1/env/2",
				Value: map[string]interface{}{
					"name":  webhook.EnvKeyGoogleApplicationCredentials,
					"value": "/var/run/secrets/workload-identity/google-application-credentials.json",
				},
			},
		))
	})

	It("injects the volume mount in all containers of the pod", func() {
		Expect(response.AdmissionResponse.Allowed).To(BeTrue())
		Expect(response.Patches).To(ContainElements(
			jsonpatch.Operation{
				Operation: "add",
				Path:      "/spec/containers/0/volumeMounts",
				Value: []interface{}{
					map[string]interface{}{
						"name":      "workload-identity-credentials",
						"mountPath": "/var/run/secrets/workload-identity",
						"readOnly":  true,
					},
				},
			},
			jsonpatch.Operation{
				Operation: "add",
				Path:      "/spec/containers/1/volumeMounts",
				Value: []interface{}{
					map[string]interface{}{
						"name":      "workload-identity-credentials",
						"mountPath": "/var/run/secrets/workload-identity",
						"readOnly":  true,
					},
				},
			},
		))
	})

	It("injects the secret volume", func() {
		Expect(response.Allowed).To(BeTrue())
		fmt.Println(response.Patches)
		Expect(response.Patches).To(ContainElements(
			jsonpatch.Operation{
				Operation: "add",
				Path:      "/spec/volumes",
				Value: []interface{}{
					map[string]interface{}{
						"name": webhook.VolumeWorkloadIdentityName,
						"projected": map[string]interface{}{
							"defaultMode": float64(webhook.VolumeWorkloadIdentityDefaultMode),
							"sources": []interface{}{
								map[string]interface{}{
									"serviceAccountToken": map[string]interface{}{
										"path":              controllers.ServiceAccountTokenPath,
										"audience":          workloadIdentityPool,
										"expirationSeconds": float64(webhook.TokenExpirationSeconds),
									},
								},
								map[string]interface{}{
									"secret": map[string]interface{}{
										"name":     "the-service-account-google-application-credentials",
										"optional": false,
										"items": []interface{}{
											map[string]interface{}{
												"key":  controllers.SecretKeyGoogleApplicationCredentials,
												"path": webhook.GoogleApplicationCredentialsJSONPath,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		))
	})

	Context("the passed pod has already been created", func() {
		When("operation is Update", func() {
			BeforeEach(func() {
				request.Operation = admissionv1.Update
			})

			It("allows the request", func() {
				Expect(response.Allowed).To(BeTrue())
				Expect(response.Patches).To(BeEmpty())
			})
		})

		When("operation is Delete", func() {
			BeforeEach(func() {
				request.Operation = admissionv1.Delete
			})

			It("allows the request", func() {
				Expect(response.Allowed).To(BeTrue())
				Expect(response.Patches).To(BeEmpty())
			})
		})

		When("operation is Connect", func() {
			BeforeEach(func() {
				request.Operation = admissionv1.Connect
			})

			It("allows the request", func() {
				Expect(response.Allowed).To(BeTrue())
				Expect(response.Patches).To(BeEmpty())
			})
		})
	})

	When("the pod doesn't have a service account", func() {
		BeforeEach(func() {
			pod.Spec.ServiceAccountName = ""
			request.Object = encodeObject(pod)
		})

		It("denies the request", func() {
			Expect(response.AdmissionResponse.Allowed).To(BeFalse())
			Expect(response.Result).NotTo(BeNil())
			Expect(response.Result.Code).To(Equal(int32(http.StatusForbidden)))
		})
	})

	When("the service account doesn't exist", func() {
		BeforeEach(func() {
			pod.Spec.ServiceAccountName = "does-not-exist"
			request.Object = encodeObject(pod)
		})

		It("returns a 400 Bad Request", func() {
			Expect(response.AdmissionResponse.Allowed).To(BeFalse())
			Expect(response.Result).NotTo(BeNil())
			Expect(response.Result.Code).To(Equal(int32(http.StatusForbidden)))
		})
	})

	When("the context has been canceled", func() {
		It("returns a 500 Internal Server Error", func() {
			canceledCtx, cancel := context.WithCancel(ctx)
			cancel()

			canceledResult := credentialsWebhook.Handle(canceledCtx, request)
			Expect(canceledResult.AdmissionResponse.Allowed).To(BeFalse())
			Expect(canceledResult.Result).NotTo(BeNil())
			Expect(canceledResult.Result.Code).To(Equal(int32(http.StatusInternalServerError)))
		})
	})

	When("the request is empty", func() {
		BeforeEach(func() {
			request.Object = runtime.RawExtension{}
		})

		It("returns a 400 Bad Request", func() {
			Expect(response.AdmissionResponse.Allowed).To(BeFalse())
			Expect(response.Result).NotTo(BeNil())
			Expect(response.Result.Code).To(Equal(int32(http.StatusBadRequest)))
		})
	})
})

func encodeObject(obj interface{}) runtime.RawExtension {
	encodedObj, err := json.Marshal(obj)
	Expect(err).NotTo(HaveOccurred())

	return runtime.RawExtension{Raw: encodedObj}
}

func ensureMembershipSecretExists(gcpCluster *infra.GCPCluster) error {
	membershipSecret := &corev1.Secret{}
	ctx := context.Background()

	err := k8sClient.Get(ctx, client.ObjectKey{
		Name:      controllers.MembershipSecretName,
		Namespace: controllers.MembershipSecretNamespace,
	}, membershipSecret)

	if k8serrors.IsNotFound(err) {
		oidcJwks := []byte{}

		membership := gke.GenerateMembership(*gcpCluster, oidcJwks)
		membershipJson, err := json.Marshal(membership)

		Expect(err).To(BeNil())

		membershipSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      controllers.MembershipSecretName,
				Namespace: controllers.MembershipSecretNamespace,
				Annotations: map[string]string{
					controllers.AnnoationMembershipSecretCreatedBy: gcpCluster.Name,
					controllers.AnnotationSecretManagedBy:          controllers.SecretManagedBy,
				},
			},
			StringData: map[string]string{
				controllers.SecretKeyGoogleApplicationCredentials: string(membershipJson),
			},
		}
		err = k8sClient.Create(context.Background(), membershipSecret)

		return err
	}

	return err
}
