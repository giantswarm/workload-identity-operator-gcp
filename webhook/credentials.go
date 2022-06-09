package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	EnvKeyGoogleApplicationCredentials   = "GOOGLE_APPLICATION_CREDENTIALS"
	EnvValueGoogleApplicationCredentials = "some-path"

	AnnotationGCPServiceAccount      = "giantswarm.io/gcp-service-account"
	AnnotationWorkloadIdentityPoolID = "giantswarm.io/gcp-workload-identity-pool-id"

	LabelWorkloadIdentity = "giantswarm.io/gcp-workload-identity"

	VolumeWorkloadIdentityName        = "workload-identity-credentials"
	VolumeWorkloadIdentityDefaultMode = 420
	VolumeMountWorkloadIdentityName   = "workload-identity"
	VolumeMountWorkloadIdentityPath   = "/var/run/secrets/workload-identity"

	TokenExpirationSeconds                   = 7200
	ServiceAccountTokenPath                  = "token"
	GoogleApplicationCredentialsJSONPath     = "google-application-credentials.json"
	ConfigMapKeyGoogleApplicationCredentials = "config"
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

	serviceAccount, err := w.getServiceAccount(ctx, pod)
	if k8serrors.IsNotFound(err) {
		logger.Error(err, "Pod ServiceAccount does not exist")
		return admission.Errored(http.StatusBadRequest, err)
	}
	if err != nil {
		logger.Error(err, "failed to get Pod ServicAccount")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	configMapName := fmt.Sprintf("%s-%s", pod.Spec.ServiceAccountName, "google-application-credentials")
	workloadIdentityPool, present := serviceAccount.Annotations[AnnotationWorkloadIdentityPoolID]
	if !present {
		message := fmt.Sprintf("ServiceAccount misssing %q annotation", AnnotationWorkloadIdentityPoolID)
		logger.Info(message)
		return admission.Denied(message)

	}

	mutatedPod := pod.DeepCopy()
	injectVolume(mutatedPod, workloadIdentityPool, configMapName)

	for i := range mutatedPod.Spec.Containers {
		container := &mutatedPod.Spec.Containers[i]
		injectEnvVar(container)
		injectVolumeMount(container)
	}

	return getPatchedResponse(req, mutatedPod)
}

func (w *CredentialsInjector) getServiceAccount(ctx context.Context, pod *corev1.Pod) (*corev1.ServiceAccount, error) {
	serviceAccount := &corev1.ServiceAccount{}
	namespacedName := types.NamespacedName{
		Name:      pod.Spec.ServiceAccountName,
		Namespace: pod.Namespace,
	}

	err := w.client.Get(ctx, namespacedName, serviceAccount)
	if err != nil {
		return nil, err
	}

	return serviceAccount, nil
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
	credentialsEnvVar := corev1.EnvVar{
		Name:  EnvKeyGoogleApplicationCredentials,
		Value: EnvValueGoogleApplicationCredentials,
	}
	container.Env = append(container.Env, credentialsEnvVar)
}

func injectVolumeMount(container *corev1.Container) {
	credentialsMount := corev1.VolumeMount{
		Name:      VolumeMountWorkloadIdentityName,
		MountPath: VolumeMountWorkloadIdentityPath,
		ReadOnly:  true,
	}
	container.VolumeMounts = append(container.VolumeMounts, credentialsMount)
}

func injectVolume(pod *corev1.Pod, workloadIdentityPool, configMapName string) {
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: VolumeWorkloadIdentityName,
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				DefaultMode: int32ptr(VolumeWorkloadIdentityDefaultMode),
				Sources: []corev1.VolumeProjection{
					{
						ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
							Path:     ServiceAccountTokenPath,
							Audience: workloadIdentityPool,

							// According to documentation, the service account token will be
							// rotated automatically by the kubelet when it's close to
							// expiring.
							// See https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/#service-account-token-volume-projection
							ExpirationSeconds: int64ptr(TokenExpirationSeconds),
						},
					},
					{
						ConfigMap: &corev1.ConfigMapProjection{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: configMapName,
							},
							Optional: boolptr(false),
							Items: []corev1.KeyToPath{
								{
									Key:  ConfigMapKeyGoogleApplicationCredentials,
									Path: GoogleApplicationCredentialsJSONPath,
								},
							},
						},
					},
				},
			},
		},
	})
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
