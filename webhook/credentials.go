package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/giantswarm/to"
	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
)

const (
	EnvKeyGoogleApplicationCredentials = "GOOGLE_APPLICATION_CREDENTIALS" //#nosec G101

	LabelWorkloadIdentity = "giantswarm.io/gcp-workload-identity"

	VolumeWorkloadIdentityName        = "workload-identity-credentials"
	VolumeWorkloadIdentityDefaultMode = 420

	TokenExpirationSeconds               = 7200
	GoogleApplicationCredentialsJSONPath = "google-application-credentials.json"
)

type CredentialsInjector struct {
	client  client.Client
	decoder *admission.Decoder
}

func NewCredentialsInjector(client client.Client, decoder *admission.Decoder) *CredentialsInjector {
	return &CredentialsInjector{
		client:  client,
		decoder: decoder,
	}
}

func (w *CredentialsInjector) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := w.getLogger(ctx)

	logger.Info("Handling admission request")
	defer logger.Info("Done")

	if req.Operation != admissionv1.Create {
		message := "pod already created"
		logger.Info(message)
		return admission.Allowed(message)
	}

	pod := &corev1.Pod{}
	err := w.decoder.Decode(req, pod)
	if err != nil {
		logger.Error(err, "no Pod in admission request")
		return admission.Errored(http.StatusBadRequest, err)
	}

	if pod.Spec.ServiceAccountName == "" {
		message := "Pod has no ServiceAccount"
		logger.Info(message)
		return admission.Denied(message)
	}

	secretName := fmt.Sprintf("%s-%s", pod.Spec.ServiceAccountName, "google-application-credentials")
	membership, err := controllers.GetMembershipFromSecret(ctx, w.client, logger)
	if err != nil {
		logger.Error(err, "failed to get membership from secret")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	workloadIdentityPool := membership.WorkloadIdentityPool

	mutatedPod := pod.DeepCopy()
	injectVolume(mutatedPod, workloadIdentityPool, secretName)

	for i := range mutatedPod.Spec.Containers {
		container := &mutatedPod.Spec.Containers[i]
		injectEnvVar(container)
		injectVolumeMount(container)
	}

	return getPatchedResponse(req, mutatedPod)
}

func (w *CredentialsInjector) getLogger(ctx context.Context) logr.Logger {
	logger := log.FromContext(ctx)
	return logger.WithName("credentials-injector-webhook")
}

func getPatchedResponse(req admission.Request, mutatedPod *corev1.Pod) admission.Response {
	marshaledPod, err := json.Marshal(mutatedPod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func injectEnvVar(container *corev1.Container) {
	credentialsPath := fmt.Sprintf("%s/%s", controllers.VolumeMountWorkloadIdentityPath, GoogleApplicationCredentialsJSONPath)

	credentialsEnvVar := corev1.EnvVar{
		Name:  EnvKeyGoogleApplicationCredentials,
		Value: credentialsPath,
	}
	container.Env = append(container.Env, credentialsEnvVar)
}

func injectVolumeMount(container *corev1.Container) {
	credentialsMount := corev1.VolumeMount{
		Name:      VolumeWorkloadIdentityName,
		MountPath: controllers.VolumeMountWorkloadIdentityPath,
		ReadOnly:  true,
	}
	container.VolumeMounts = append(container.VolumeMounts, credentialsMount)
}

func injectVolume(pod *corev1.Pod, workloadIdentityPool, secretName string) {
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: VolumeWorkloadIdentityName,
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				DefaultMode: to.Int32P(VolumeWorkloadIdentityDefaultMode),
				Sources: []corev1.VolumeProjection{
					{
						ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
							Path:     controllers.ServiceAccountTokenPath,
							Audience: workloadIdentityPool,

							// According to documentation, the service account token will be
							// rotated automatically by the kubelet when it's close to
							// expiring.
							// See https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/#service-account-token-volume-projection
							ExpirationSeconds: to.Int64P(TokenExpirationSeconds),
						},
					},
					{
						Secret: &corev1.SecretProjection{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: secretName,
							},
							Items: []corev1.KeyToPath{
								{
									Key:  controllers.SecretKeyGoogleApplicationCredentials,
									Path: GoogleApplicationCredentialsJSONPath,
								},
							},
							Optional: to.BoolP(false),
						},
					},
				},
			},
		},
	})
}
