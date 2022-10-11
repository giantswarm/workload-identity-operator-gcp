package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/fleet-membership-operator-gcp/types"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
)

func GetEnvOrSkip(env string) string {
	value := os.Getenv(env)
	if value == "" {
		ginkgo.Skip(fmt.Sprintf("%s not exported", env))
	}

	return value
}

func EnsureMembershipSecretExists(k8sClient client.Client, workloadIdentityPool, identityProvider string) {
	membership := types.MembershipData{
		WorkloadIdentityPool: workloadIdentityPool,
		IdentityProvider:     identityProvider,
	}
	membershipJson, err := json.Marshal(membership)
	Expect(err).To(Succeed())

	membershipSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      controllers.MembershipSecretName,
			Namespace: controllers.DefaultMembershipSecretNamespace,
		},
		StringData: map[string]string{
			controllers.SecretKeyGoogleApplicationCredentials: string(membershipJson),
		},
	}
	err = k8sClient.Create(context.Background(), membershipSecret)
	if !k8serrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}
