package serviceaccount

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/giantswarm/workload-identity-operator-gcp/webhook"
)

const (
	AnnotationSecretMetadata  = "kubernetes.io/service-account.name" //#nosec G101
	AnnotationSecretManagedBy = "app.kubernetes.io/managed-by"       //#nosec  G101

	SecretManagedBy = "workload-identity-operator-gcp" //#nosec G101

	SecretNameSuffix = "google-application-credentials" //#nosec G101
)

type ServiceAccountReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Logger logr.Logger
}

//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=serviceaccounts/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=serviceaccounts/finalizers,verbs=update

func (r *ServiceAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Logger.WithValues("service-account", req.NamespacedName)

	serviceAccount := &corev1.ServiceAccount{}

	err := r.Get(ctx, req.NamespacedName, serviceAccount)
	if err != nil {
		logger.Error(err, "could not get service account")
		return reconcile.Result{}, nil
	}

	gcpServiceAccount, isGCPAnnotated := serviceAccount.Annotations[webhook.AnnotationGCPServiceAccount]
	workloadIdentityPool, hasWorkloadIdentity := serviceAccount.Annotations[webhook.AnnotationWorkloadIdentityPoolID]
	identityProvider, hasIdentityProvider := serviceAccount.Annotations[webhook.AnnotationGCPIdentityProvider]

	if !isGCPAnnotated {
		message := fmt.Sprintf("Skipping ServiceAccount missing %q annotation", webhook.AnnotationGCPServiceAccount)
		logger.Info(message)
		return reconcile.Result{}, err
	}

	if !hasWorkloadIdentity {
		message := fmt.Sprintf("Skipping ServiceAccount missing %q annotation", webhook.AnnotationWorkloadIdentityPoolID)
		logger.Info(message)
		return reconcile.Result{}, err
	}

	if !hasIdentityProvider {
		message := fmt.Sprintf("Skipping ServiceAccount missing %q annotation", webhook.AnnotationGCPIdentityProvider)
		logger.Info(message)
		return reconcile.Result{}, err
	}

	secretName := fmt.Sprintf("%s-%s", serviceAccount.Name, SecretNameSuffix)
	secret := &corev1.Secret{}

	err = r.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: req.Namespace,
	}, secret)

	if err != nil && !k8serrors.IsNotFound(err) {
		logger.Error(err, "failed to get secret")
		return reconcile.Result{}, err
	}

	data := fmt.Sprintf(`{
	     "type": "external_account",
	     "audience": "identitynamespace:%[1]s:%[2]s",
	     "service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/%[3]s:generateAccessToken",
	     "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
	     "token_url": "https://sts.googleapis.com/v1/token",
	     "credential_source": {
	       "file": "/var/run/secrets/tokens/gcp-ksa/token"
	     }
	   }`, workloadIdentityPool, identityProvider, gcpServiceAccount)

	newSecret, err := r.generateNewSecret(serviceAccount, secretName, data)
	if err != nil {
		logger.Error(err, "failed to generate new secret")
		return reconcile.Result{}, err
	}

	if !secret.CreationTimestamp.IsZero() {
		err = r.updateSecret(ctx, newSecret)
		return reconcile.Result{}, err
	}

	err = r.createSecret(ctx, newSecret)

	return ctrl.Result{}, err
}

func (r *ServiceAccountReconciler) updateSecret(ctx context.Context, secret *corev1.Secret) error {
	err := r.Update(ctx, secret)
	if err != nil {
		r.Logger.Error(err, "failed to update google application credentials json secret")
		return err
	}

	return nil
}

func (r *ServiceAccountReconciler) createSecret(ctx context.Context, secret *corev1.Secret) error {
	err := r.Create(ctx, secret)
	if err != nil {
		r.Logger.Error(err, "failed to create google application credentials json secret")
		return err
	}

	return nil
}

func (r *ServiceAccountReconciler) generateNewSecret(serviceAccount *corev1.ServiceAccount, name, data string) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: serviceAccount.Namespace,
			Annotations: map[string]string{
				AnnotationSecretMetadata:  serviceAccount.Name,
				AnnotationSecretManagedBy: SecretManagedBy,
			},
		},
		StringData: map[string]string{
			webhook.SecretKeyGoogleApplicationCredentials: data,
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}

	err := controllerutil.SetOwnerReference(serviceAccount, secret, r.Scheme)
	if err != nil {
		r.Logger.Error(err, "failed to set owner reference on secret")
		return &corev1.Secret{}, err
	}

	return secret, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ServiceAccount{}).
		Complete(r)
}
