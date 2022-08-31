package controllers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	gkehubpb "google.golang.org/genproto/googleapis/cloud/gkehub/v1beta1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	AnnotationSecretMetadata    = "kubernetes.io/service-account.name" //#nosec G101
	AnnotationSecretManagedBy   = "app.kubernetes.io/managed-by"       //#nosec  G101
	AnnotationGCPServiceAccount = "giantswarm.io/gcp-service-account"

	SecretManagedBy = "workload-identity-operator-gcp" //#nosec G101

	SecretNameSuffix                      = "google-application-credentials" //#nosec G101
	SecretKeyGoogleApplicationCredentials = "config"

	ServiceAccountTokenPath         = "token"
	VolumeMountWorkloadIdentityPath = "/var/run/secrets/workload-identity"
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

	gcpServiceAccount, isGCPAnnotated := serviceAccount.Annotations[AnnotationGCPServiceAccount]

	if !isGCPAnnotated {
		message := fmt.Sprintf("Skipping ServiceAccount missing %q annotation", AnnotationGCPServiceAccount)
		logger.Info(message)
		return reconcile.Result{}, err
	}

	membership, err := GetMembershipFromSecret(ctx, r.Client, logger)
	if err != nil {
		logger.Error(err, "failed to get membership from secret")
		return reconcile.Result{}, err
	}

	identityProvider := membership.Authority.IdentityProvider
	workloadIdentityPool := membership.Authority.WorkloadIdentityPool

	if identityProvider == "" || workloadIdentityPool == "" {
		err = fmt.Errorf("membership not configured properly %+v", membership)
		logger.Error(err, "membership not configured properly")
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
	       "file": "%[4]s/%[5]s"
	     }
	   }`,
		workloadIdentityPool, identityProvider, gcpServiceAccount,
		VolumeMountWorkloadIdentityPath,
		ServiceAccountTokenPath)

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

func GetMembershipFromSecret(ctx context.Context, c client.Client, logger logr.Logger) (*gkehubpb.Membership, error) {
	secret := &corev1.Secret{}

	err := c.Get(ctx, client.ObjectKey{
		Namespace: MembershipSecretNamespace,
		Name:      MembershipSecretName,
	}, secret)

	if err != nil {
		logger.Error(err, "failed to get membership secret")
		return nil, err
	}

	data := secret.Data[SecretKeyGoogleApplicationCredentials]

	membership := &gkehubpb.Membership{}
	err = json.Unmarshal(data, membership)

	return membership, err
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
			SecretKeyGoogleApplicationCredentials: data,
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
