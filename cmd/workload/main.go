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
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/go-logr/logr"

	"github.com/giantswarm/workload-identity-operator-gcp/cmd"
	"github.com/giantswarm/workload-identity-operator-gcp/webhook"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/giantswarm/workload-identity-operator-gcp/controllers"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var enableClusterReconciler bool
	var probeAddr string
	var webhookPort int
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "The port for the webhook")
	flag.BoolVar(&enableClusterReconciler, "enable-cluster-reconciler", false,
		"Enable the GCPCluster reconciler. This should be enabled only on Management Clusters.")

	options := ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Host:                   "0.0.0.0",
		Port:                   webhookPort,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "workload-identity-operator-gcp.giantswarm.io",
		CertDir:                "/etc/webhook/certs",
	}

	cmd.StartControllerManager(options, wireWorkloadIdentityOperator)

	//+kubebuilder:scaffold:builder
}

func wireWorkloadIdentityOperator(logger logr.Logger, mgr manager.Manager) func() {
	reconciler := &controllers.ServiceAccountReconciler{
		Client: mgr.GetClient(),
		Logger: ctrl.Log.WithName("service-account-reconciler"),
		Scheme: mgr.GetScheme(),
	}

	if err := reconciler.SetupWithManager(mgr); err != nil {
		logger.Error(err, "unable to create controller", "controller", "ServiceAccount")
		os.Exit(1)
	}

	decoder, err := admission.NewDecoder(scheme)
	if err != nil {
		logger.Error(err, "Failed to create admission decoder")
		os.Exit(1)
	}

	mgr.GetWebhookServer().Register("/", &admission.Webhook{
		Handler: webhook.NewCredentialsInjector(mgr.GetClient(), decoder),
	})

	return func() {}
}
