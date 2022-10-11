# workload-identity-operator-gcp
Operator to automate workload identity setup on GCP clusters

## Introduction

[Workload identity](https://cloud.google.com/iam/docs/workload-identity-federation) gives a workload app the ability to exchange an OAuth token, acquired from some external identity provider, for a GCP access token.
The application can use this access token to use whatever resources it needs on GCP.


> Workload Identity allows a Kubernetes service account in your GKE cluster to act as an IAM service account.
> Pods that use the configured Kubernetes service account automatically authenticate as the IAM service account when accessing Google Cloud APIs.
> Using Workload Identity allows you to assign distinct, fine-grained identities and authorization for each application in your cluster.


## Prerequisites
1. Install [gcloud](https://cloud.google.com/sdk/docs/install)
2. A cluster on GCP. [Creating a cluster ](https://github.com/giantswarm/capo-mc-bootstrap/)
3. [Enabling the GKE API](https://cloud.google.com/endpoints/docs/openapi/enable-api) on your GCP Project. 
4. A registered membership on GCP. See [fleet-membership-operator-gcp](https://github.com/giantswarm/fleet-membership-operator-gcp)


## Usage

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

##### 3 Give the Kubernetes Service Account permission to impersonate the GCP Service Account

```
  export GOOGLE_SA_ID="$GOOGLE_SA_NAME@$GCP_PROJECT_NAME.iam.gserviceaccount.com"

  # this policy binding associates the GCP service account with a Kubernetes service account
  gcloud iam service-accounts add-iam-policy-binding \
    --project "$GCP_PROJECT_NAME" \
    "$GOOGLE_SA_ID" \
    --role=roles/iam.workloadIdentityUser \
    --member="serviceAccount:$WORKLOAD_ID_POOL[$KUBE_NAMESPACE/$KUBE_SA_NAME]"
```

##### 4 Ensure that your GCP service account has the roles that the workload will need.

Example: Add the `compute.viewer` role:
```
  # Add necessary permissions to the GCP Service Account
  gcloud projects add-iam-policy-binding "$GCP_PROJECT_NAME" \
    --role=roles/compute.viewer \
    --member="serviceAccount:$GOOGLE_SA_ID"
```

##### 5 Annotate kubernetes service account
  ```
  kubectl annotate sa $KUBE_SA_NAME \ 
  giantswarm.io/gcp-service-account=$GOOGLE_SA_ID \
  ```

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


