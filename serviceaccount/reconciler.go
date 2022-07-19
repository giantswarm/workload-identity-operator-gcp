package serviceaccount

import (
	"context"
	"fmt"

	"github.com/giantswarm/to"
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
	serviceAccount := &corev1.ServiceAccount{}

	err := r.Client.Get(ctx, req.NamespacedName, serviceAccount)
	if err != nil {
		r.Logger.Error(err, "could not get service account", "service account", req.NamespacedName)
		return reconcile.Result{}, nil
	}

	gcpServiceAccount, isGCPAnnotated := serviceAccount.Annotations[webhook.AnnotationGCPServiceAccount]
	workloadIdentityPool, hasWorkloadIdentity := serviceAccount.Annotations[webhook.AnnotationWorkloadIdentityPoolID]
	identityProvider, hasIdentityProvider := serviceAccount.Annotations[webhook.AnnotationGCPIdentityProvider]

	if !isGCPAnnotated {
		message := fmt.Sprintf("Skipping ServiceAccount missing %q annotation", webhook.AnnotationGCPServiceAccount)
		r.Logger.Info(message, "service-account", req.NamespacedName)
		return reconcile.Result{}, err
	}

	if !hasWorkloadIdentity {
		message := fmt.Sprintf("Skipping ServiceAccount missing %q annotation", webhook.AnnotationWorkloadIdentityPoolID)
		r.Logger.Info(message, "service-account", req.NamespacedName)
		return reconcile.Result{}, err
	}

	if !hasIdentityProvider {
		message := fmt.Sprintf("Skipping ServiceAccount missing %q annotation", webhook.AnnotationGCPIdentityProvider)
		r.Logger.Info(message, "service-account", req.NamespacedName)
		return reconcile.Result{}, err
	}

	secretName := fmt.Sprintf("%s-%s", serviceAccount.Name, SecretNameSuffix)
	secret := &corev1.Secret{}

	err = r.Client.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: req.Namespace,
	}, secret)

	if err != nil && !k8serrors.IsNotFound(err) {
		r.Logger.Error(err, "secret already exists", "service-account", req.NamespacedName)
		return reconcile.Result{}, err
	}

	// Secret already exists, no need to create it again
	if !secret.CreationTimestamp.IsZero() {
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

	err = r.createSecret(ctx, serviceAccount, secretName, data)

	return ctrl.Result{}, err
}

func (r *ServiceAccountReconciler) createSecret(ctx context.Context, serviceAccount *corev1.ServiceAccount, name, data string) error {
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
		Immutable: to.BoolP(true),
		Type:      corev1.SecretTypeServiceAccountToken,
	}

	err := controllerutil.SetOwnerReference(serviceAccount, secret, r.Scheme)
	if err != nil {
		r.Logger.Error(err, "failed to set owner reference on secret")
		return err
	}

	err = r.Client.Create(ctx, secret)
	if err != nil {
		r.Logger.Error(err, "failed to create google application credentials json secret")
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ServiceAccount{}).
		Complete(r)
}
