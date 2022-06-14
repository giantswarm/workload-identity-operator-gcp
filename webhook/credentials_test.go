package webhook_test

import (
	"context"
	"encoding/json"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gomodules.xyz/jsonpatch/v2"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/giantswarm/workload-identity-operator-gcp/webhook"
)

var _ = Describe("Credentials", func() {
	var (
		ctx                context.Context
		credentialsWebhook *webhook.CredentialsInjector

		pod            corev1.Pod
		serviceAccount *corev1.ServiceAccount
		request        admission.Request
		response       admission.Response
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
					webhook.AnnotationGCPServiceAccount:      "service-account@email",
					webhook.AnnotationWorkloadIdentityPoolID: "workload-identity-pool-id",
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

		request = admission.Request{
			AdmissionRequest: admissionv1.AdmissionRequest{
				Object:    encodeObject(pod),
				Operation: admissionv1.Create,
			},
		}
	})

	JustBeforeEach(func() {
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
										"path":              webhook.ServiceAccountTokenPath,
										"audience":          "workload-identity-pool-id",
										"expirationSeconds": float64(webhook.TokenExpirationSeconds),
									},
								},
								map[string]interface{}{
									"configMap": map[string]interface{}{
										"name":     "the-service-account-google-application-credentials",
										"optional": false,
										"items": []interface{}{
											map[string]interface{}{
												"key":  webhook.ConfigMapKeyGoogleApplicationCredentials,
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

	When("the service account isn't annotated with the workload identity id", func() {
		BeforeEach(func() {
			modified := serviceAccount.DeepCopy()
			modified.Annotations = map[string]string{}
			Expect(k8sClient.Patch(ctx, modified, client.MergeFrom(serviceAccount))).To(Succeed())
		})

		It("denies the request", func() {
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

func int32ptr(i int) *int32 {
	u := int32(i)
	return &u
}

func int64ptr(i int) *int64 {
	u := int64(i)
	return &u
}

func boolptr(b bool) *bool {
	return &b
}
