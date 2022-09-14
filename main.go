/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	gkehub "cloud.google.com/go/gkehub/apiv1beta1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	serviceaccount "github.com/giantswarm/workload-identity-operator-gcp/controllers"
	gke "github.com/giantswarm/workload-identity-operator-gcp/pkg/gke/membership"
	"github.com/giantswarm/workload-identity-operator-gcp/webhook"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	infra "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(infra.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var webhookPort int
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "The port for the webhook")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Host:                   "0.0.0.0",
		Port:                   webhookPort,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "workload-identity-operator-gcp.giantswarm.io",
		CertDir:                "/etc/webhook/certs",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&serviceaccount.ServiceAccountReconciler{
		Client: mgr.GetClient(),
		Logger: ctrl.Log.WithName("service-account-reconciler"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ServiceAccount")
		os.Exit(1)
	}

	ctx := context.Background()
	gkehubClient, err := gkehub.NewGkeHubMembershipRESTClient(ctx)
	if err != nil {
		setupLog.Error(err, "failed to create gke hub membership client")
		os.Exit(1)
	}

	defer gkehubClient.Close()

	gkeClient := gke.NewClient(gkehubClient)
	gkeMembershipReconciler := gke.NewGKEClusterReconciler(
		gkeClient,
		ctrl.Log.WithName("gke-membership-reconciler"),
	)

	if err = (&controllers.GCPClusterReconciler{
		Client:                  mgr.GetClient(),
		Logger:                  ctrl.Log.WithName("gcp-cluster-reconciler"),
		Scheme:                  mgr.GetScheme(),
		GKEMembershipReconciler: gkeMembershipReconciler,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "GCPCluster")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	decoder, err := admission.NewDecoder(scheme)
	if err != nil {
		exitfIfError(err, "Failed to create admission decoder")
	}

	mgr.GetWebhookServer().Register("/", &admission.Webhook{
		Handler: webhook.NewCredentialsInjector(mgr.GetClient(), decoder),
	})

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func exitfIfError(err error, message string) {
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("%s: %w", message, err))
		os.Exit(1)
	}
}
