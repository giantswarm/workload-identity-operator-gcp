# workload-identity-operator-gcp
Operator to automate workload identity setup on GCP clusters

## Introduction

[Workload identity](https://cloud.google.com/iam/docs/workload-identity-federation) gives a workload app the ability to exchange an OAuth token, acquired from some external identity provider, for a GCP access token.
The application can use this access token to use whatever resources it needs on GCP.


> Workload Identity allows a Kubernetes service account in your GKE cluster to act as an IAM service account.
> Pods that use the configured Kubernetes service account automatically authenticate as the IAM service account when accessing Google Cloud APIs.
> Using Workload Identity allows you to assign distinct, fine-grained identities and authorization for each application in your cluster.

## Concepts

This operator is set up to work on a multi-cluster setup. What is a multi-cluster setup?

> Multi-cluster Kubernetes is a Kubernetes deployment method that consists of two or more clusters.

It operates on the notion of management and workload clusters. Where a management cluster manages the lifecycle of a workload cluster and vice versa.


## Prerequisites
1. Install [gcloud](https://cloud.google.com/sdk/docs/install)
2. A cluster on GCP. [Creating a cluster ](https://github.com/giantswarm/capo-mc-bootstrap/)
3. [Enabling the GKE API](https://cloud.google.com/endpoints/docs/openapi/enable-api) on your GCP Project. 


## Usage

### Steps to take on a management cluster
These are steps that are meant to be executed on a management cluster before the configuration steps are done.

Note, when you enable Workload Identity on a cluster, GKE automatically creates a **fixed** workload identity pool for the cluster's **Google Cloud Project**.
A workload identity pool allows IAM to understand and trust Kubernetes service account credentials.
The workload identity pool has the following format:
```
  PROJECT_ID.svc.id.goog
```

#### 1. Register your cluster
```
export CLUSTER_NAME="<insert-cluster-name-here>" 
export MEMBERSHIP_NAME="$CLUSTER_NAME-workload-identity"
export GCP_PROJECT_NAME="<insert-project-name-here>"
export KUBECONFIG_CONTEXT="<insert-context-here>"
export KUBECONFIG_PATH="~/.kube/config"
```
```
gcloud container hub memberships register "$MEMBERSHIP_NAME" \
    --project "$GCP_PROJECT_NAME" \
    --context="$KUBECONFIG_CONTEXT" \
    --kubeconfig="$KUBECONFIG_PATH" \
    --enable-workload-identity \
    --has-private-issuer
``` 

### Steps on workload clusters
These are steps that are meant to be taken on the workload cluster before the configuration steps

#### 1. Create a workload cluster with the following annotation
```
giantswarm.io/workload-identity-enabled: "true"
```
💡 It expects to find the GCP project id in the spec of the workload cluster


### Configuration

##### 1 Create a kubernetes service account

```
export KUBE_SA_NAME="<inset-sa-name-here>"
export KUBE_NAMESPACE="<insert-namespace-here>"

kubectl create serviceaccount $KUBE_SA_NAME \
    --namespace $KUBE_NAMESPACE
```

##### 2 [Create a GCP service account](https://cloud.google.com/iam/docs/creating-managing-service-accounts#creating) or 

```
export GOOGLE_SA_NAME="<insert-gcp-service-account-name-here>"
gcloud iam service-accounts create "$GOOGLE_SA_NAME" --project="$GCP_PROJECT_NAME"
```

##### 3 Grab important information that you'll need. 
These are the following:
  * Your workload identity pool id
  * Your identity provider 
The above can be obtained from the output of the command below
```
gcloud container hub memberships describe $MEMBERSHIP_NAME
```

##### 4 Ensure that your GCP service account has the roles that you need. 

```
  export GOOGLE_SA_ID="$GOOGLE_SA_NAME@$GCP_PROJECT_NAME.iam.gserviceaccount.com"

  # this policy binding associates the GCP service account with a Kubernetes service account
  gcloud iam service-accounts add-iam-policy-binding \
    --project "$GCP_PROJECT_NAME" \
    "$GOOGLE_SA_ID" \
    --role=roles/iam.workloadIdentityUser \
    --member="serviceAccount:$WORKLOAD_ID_POOL[$KUBE_NAMESPACE/$KUBE_SA_NAME]"
```

##### 5 Add computer viewer permissions
```
  # Add necessary permissions to the GCP Service Account
  gcloud projects add-iam-policy-binding "$GCP_PROJECT_NAME" \
    --role=roles/compute.viewer \
    --member="serviceAccount:$GOOGLE_SA_ID"
```

##### 6 Annotate kubernetes service account
  ```
  kubectl annotate sa $KUBE_SA_NAME \ 
  giantswarm.io/gcp-service-account=$GOOGLE_SA_ID \
  giantswarm.io/gcp-workload-identity=$WORKLOAD_ID_POOL \
  giantswarm.io/gcp-identity-provider=$IDENTITY_PROVIDER
  ```

### The GCP Cluster Reconciler
The reconciler tracks `GCPClusters` that are created on a cluster. It is assumed that any cluster that this resource is created on, is a management cluster.
It will check for the annotation `giantswarm.io/workload-identity-enabled: "true"` and whether the cluster has a node ready.
It'll then create a `Secret` called `workload-identity-operator-gcp-membership` in the **giantswarm** `namespace` which contains the information needed by the service account reconciler.
This includes the following:
  * The Authority Issuer
  * The Workload Identity Pool ID
  * The Identity Provider
  * The OIDC JWKS of the cluster API server

### The Service Account Reconciler

The reconciler tracks `ServiceAccounts` annotated with `giantswarm.io/gcp-service-account`, `giantswarm.io/gcp-workload-identity` & `giantswarm.io/gcp-identity-provider` which should contain the GCP service account ID (in the format `<gcp-service-account-name>@<gcp-project-name>.iam.gserviceaccount.com`).
When it notices such a `ServiceAccount`, it will create a `Secret` with the `GOOGLE_APPLICATION_CREDENTIALS` json file, which has the following format:
```json
{
      "type": "external_account",
      "audience": "identitynamespace:<workload-identity-pool-id>:<identity-provider-from-workload-identity-pool>",
      "service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/<service-account-id>:generateAccessToken",
      "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
      "token_url": "https://sts.googleapis.com/v1/token",
      "credential_source": {
        "file": "/var/run/secrets/tokens/gcp-ksa/token"
      }
}
```
These credentials will be used by the pod's GCP SDK library to perform the token exchange, swapping the Kubernetes ServiceAccount token for a GCP one.

### Webhook

The webhook injects the necessary volumes and env variable to a pod labelled with: `giantswarm.io/workload-identity: "true"`.
The label is there so it doesn't interfere with normal Pod creation.
If the pod is labelled and it also has a `ServiceAccount`, that has the annotation `giantswarm.io/gcp-service-account`, it will inject the env variable:


