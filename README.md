# virtual-garden-kubeconfig-refresher

Can be used to establish a connection to a Garden cluster and retrieve the current kubeconfig for the virtual garden with a token managed by the `virtual-garden-access` managed resource. The approach using the Gardener Resource Manager for this kubeconfig is described [here](https://gardener.cloud/docs/gardener/concepts/operator/#virtual-garden-kubeconfig). Here is the link to the deployment through metal-stack: https://github.com/metal-stack/metal-roles/tree/master/control-plane/roles/gardener-virtual-garden-access.

The program writes the resulting kubeconfig + token file into the file system, such that it can also be used outside of Kubernetes clusters.

If an in-cluster Kubernetes client can be constructed during runtime, the program also writes the kubeconfig to a secret in a given namespace. In this case, other pods can be mount this secret to gain access to the virtual garden, too.

The Garden cluster has to be hosted at metalstack.cloud or Google. In both cases the connected account needs permissions to retrieve cluster credentials for the referenced cluster entity.

## Configuration

| Env                            | Description                                                                 |
| ------------------------------ | --------------------------------------------------------------------------- |
| TOKEN_FILE_PATH                | The path where the token gets written to                                    |
| KUBECONFIG_FILE_PATH           | The path where the kubeconfig gets written to                               |
| SECRET_NAME                    | The name of the secret to write the kubeconfig and token to                 |
| NAMESPACE                      | The namespace in which the secret resides                                   |
| REFRESH_BEFORE                 | The duration to renew the token before the current kubeconfig token expires |
| METAL_STACK_CLOUD_API_TOKEN    | The metalstack.cloud API token  to retrieve cluster credentials with        |
| METAL_STACK_CLOUD_PROJECT_ID   | The metalstack.cloud project id used for retrieving cluster credentials     |
| METAL_STACK_CLOUD_CLUSTER_ID   | The metalstack.cloud cluster id used for retrieving cluster credentials     |
| GOOGLE_APPLICATION_CREDENTIALS | The GCP service account JSON to retrieve cluster credentials with           |
| GOOGLE_PROJECT_ID              | The GCP project id used for retrieving cluster credentials                  |
| GOOGLE_LOCATION                | The GCP location used for retrieving cluster credentials                    |
| GOOGLE_CLUSTER_NAME            | The GCP cluster name used for retrieving cluster credentials                |
