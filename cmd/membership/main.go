/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and limitations under the License. */

package main

import (
	"context"
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	gkehub "cloud.google.com/go/gkehub/apiv1beta1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/go-logr/logr"

	"github.com/giantswarm/workload-identity-operator-gcp/cmd"
	gke "github.com/giantswarm/workload-identity-operator-gcp/pkg/gke/membership"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	capg "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	kubeadm "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	//+kubebuilder:scaffold:imports
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(capg.AddToScheme(scheme))
	utilruntime.Must(kubeadm.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	options := ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "fleet-membership-operator.giantswarm.io",
	}

	cmd.StartControllerManager(options, wireGCPClusterReconciler)
	//+kubebuilder:scaffold:builder
}

func wireGCPClusterReconciler(logger logr.Logger, mgr manager.Manager) func() {
	ctx := context.Background()
	gkehubClient, err := gkehub.NewGkeHubMembershipRESTClient(ctx)
	if err != nil {
		logger.Error(err, "failed to create gke hub membership client")
		os.Exit(1)
	}

	gkeClient := gke.NewClient(gkehubClient)
	gkeMembershipReconciler := gke.NewGKEClusterReconciler(
		gkeClient,
		ctrl.Log.WithName("gke-membership-reconciler"),
	)

	reconciler := &controllers.GCPClusterReconciler{
		Client:                    mgr.GetClient(),
		Logger:                    ctrl.Log.WithName("gcp-cluster-reconciler"),
		MembershipSecretNamespace: controllers.DefaultMembershipSecretNamespace,
		GKEMembershipReconciler:   gkeMembershipReconciler,
	}

	if err = reconciler.SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "GCPCluster")
		os.Exit(1)
	}

	return func() {
		err := gkehubClient.Close()
		if err != nil {
			logger.Error(err, "failed to close GKEHub Client connection")
		}
	}
}
