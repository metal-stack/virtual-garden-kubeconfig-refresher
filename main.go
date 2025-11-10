package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"time"

	"connectrpc.com/connect"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	configlatest "k8s.io/client-go/tools/clientcmd/api/latest"
	configv1 "k8s.io/client-go/tools/clientcmd/api/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/metal-stack-cloud/api/go/api/v1"
	mscclient "github.com/metal-stack-cloud/api/go/client"

	gcpv1 "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"

	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

var (
	tokenFilePath      = envOrDefault("TOKEN_FILE_PATH", "token")
	kubeconfigFilePath = envOrDefault("KUBECONFIG_FILE_PATH", "kubeconfig")
	secretName         = envOrDefault("SECRET_NAME", "virtual-garden-kubeconfig")
	namespace          = os.Getenv("NAMESPACE")
)

type refresher struct {
	log *slog.Logger
	ctx context.Context
}

func envOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func main() {
	var (
		log         = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
		ctx, cancel = signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	)
	defer cancel()

	r := &refresher{
		log: log,
		ctx: ctx,
	}

	if err := r.run(); err != nil {
		log.Error("error during runtime", "error", err)
		os.Exit(1)
	}
}

func (r *refresher) run() error {
	r.log.Info("refreshing kubeconfig", "kubeconfig-path", kubeconfigFilePath, "token-file-path", tokenFilePath)

	next, err := r.refresh(false)
	if err != nil {
		return err
	}

	timer := time.NewTimer(*next)

	r.log.Info("waiting for next timer")

	for {
		select {
		case <-r.ctx.Done():
			r.log.Info("retrieved signal, exiting...")
			return nil
		case <-timer.C:
			r.log.Info("start refreshing kubeconfig after timer channel message was received")

			next, err := r.refresh(true)
			if err != nil {
				return err
			}

			timer = time.NewTimer(*next)

			r.log.Info("waiting for next timer")
		}
	}
}

func (r *refresher) refresh(withBackoff bool) (*time.Duration, error) {
	gc, err := r.getGardenClusterClient()
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve garden cluster client: %w", err)
	}

	vgc, err := getVirtualGardenClient(r.ctx, gc)
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve virtual garden cluster client: %w", err)
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigFilePath)
	if err != nil {
		return nil, fmt.Errorf("unable to build config: %w", err)
	}

	c, err := client.New(config, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("unable to create virtual garden client: %w", err)
	}

	var secretList corev1.SecretList
	err = c.List(r.ctx, &secretList)
	if err != nil {
		return nil, fmt.Errorf("resulting kubeconfig does not work for listing secrets: %w", err)
	}

	r.log.Info("resulting kubeconfig works")

	for _, p := range []struct {
		path    string
		content string
	}{
		{
			path:    kubeconfigFilePath,
			content: vgc.kubeconfig,
		},
		{
			path:    tokenFilePath,
			content: vgc.token,
		},
	} {
		if err := os.MkdirAll(path.Dir(p.path), 0600); err != nil {
			return nil, fmt.Errorf("unable to create directory tree: %w", err)
		}

		if err := os.WriteFile(p.path, []byte(p.content), 0600); err != nil {
			return nil, fmt.Errorf("unable to write file: %w", err)
		}
	}

	if err := os.WriteFile(tokenFilePath, []byte(vgc.token), 0600); err != nil {
		return nil, fmt.Errorf("unable to write token: %w", err)
	}

	r.log.Info("written files", "kubeconfig-path", kubeconfigFilePath, "token-file-path", tokenFilePath)

	if cfg, err := rest.InClusterConfig(); err == nil {
		r.log.Info("detected running in kubernetes, writing back secret", "name", secretName, "namespace", namespace)

		c, err = client.New(cfg, client.Options{})
		if err != nil {
			return nil, fmt.Errorf("unable to create client to write back secret: %w", err)
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
		}

		_, err = controllerutil.CreateOrUpdate(r.ctx, c, secret, func() error {
			secret.StringData = map[string]string{
				"kubeconfig": vgc.kubeconfig,
				"token":      vgc.token,
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("unable to write back secret: %w", err)
		}

		r.log.Info("kubeconfig successfully written to kubernetes secret", "name", secretName, "namespace", namespace)
	}

	timeUntilRefresh := time.Until(vgc.renew.Add(3 * time.Minute)) // give grm three minutes for renewal
	if withBackoff && timeUntilRefresh < 5*time.Minute {
		// prevent permanent loop in case grm has issues to renew the token
		timeUntilRefresh = timeUntilRefresh + 5*time.Minute
		r.log.Warn("backoff to reduce permanent looping")
	}

	r.log.Info("token renewal scheduling", "gardener-renewal-on", vgc.renew.String(), "schedule-refresh-in", timeUntilRefresh.String())

	return &timeUntilRefresh, nil
}

func (r *refresher) getGardenClusterClient() (client.Client, error) {
	switch {
	case os.Getenv("METAL_STACK_CLOUD_API_TOKEN") != "":
		r.log.Info("using metalstack.cloud garden cluster")
		return fromMetalStackCloud(r.ctx)
	case os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "":
		r.log.Info("using GKE garden cluster")
		return fromGCP(r.ctx)
	default:
		return nil, fmt.Errorf("either METAL_STACK_CLOUD_API_TOKEN or GOOGLE_APPLICATION_CREDENTIALS must be provided")
	}
}

func fromMetalStackCloud(ctx context.Context) (client.Client, error) {
	var (
		token   = os.Getenv("METAL_STACK_CLOUD_API_TOKEN")
		project = os.Getenv("METAL_STACK_CLOUD_PROJECT_ID")
		cluster = os.Getenv("METAL_STACK_CLOUD_CLUSTER_ID")
	)

	if token == "" || project == "" || cluster == "" {
		return nil, fmt.Errorf("METAL_STACK_CLOUD_API_TOKEN, METAL_STACK_CLOUD_PROJECT_ID and METAL_STACK_CLOUD_CLUSTER_ID must be given")
	}

	c := mscclient.New(mscclient.DialConfig{
		BaseURL: "https://api.metalstack.cloud",
		Token:   token,
	})

	resp, err := c.Apiv1().Cluster().GetCredentials(ctx, connect.NewRequest(&apiv1.ClusterServiceGetCredentialsRequest{
		Project: project,
		Uuid:    cluster,
	}))
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve cluster credentials: %w", err)
	}

	clientCfg, err := clientcmd.NewClientConfigFromBytes([]byte(resp.Msg.GetKubeconfig()))
	if err != nil {
		return nil, err
	}

	rest, err := clientCfg.ClientConfig()
	if err != nil {
		return nil, err
	}

	return client.New(rest, client.Options{})
}

func fromGCP(ctx context.Context) (client.Client, error) {
	var (
		project  = os.Getenv("GOOGLE_PROJECT_ID")
		location = os.Getenv("GOOGLE_LOCATION")
		name     = os.Getenv("GOOGLE_CLUSTER_NAME")
	)

	if location == "" || project == "" || name == "" {
		return nil, fmt.Errorf("GOOGLE_PROJECT_ID, GOOGLE_LOCATION and GOOGLE_CLUSTER_NAME must be given")
	}

	c, err := gcpv1.NewClusterManagerRESTClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to create google client: %w", err)
	}

	cluster, err := c.GetCluster(ctx, &containerpb.GetClusterRequest{
		Name: fmt.Sprintf("projects/%s/locations/%s/clusters/%s", project, location, name),
	})
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve cluster: %w", err)
	}

	cert, err := base64.StdEncoding.DecodeString(cluster.GetMasterAuth().ClusterCaCertificate)
	if err != nil {
		return nil, fmt.Errorf("unable to decode ca cert: %w", err)
	}

	kubeconfig := &clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			name: {
				Server:                   "https://" + cluster.GetEndpoint(),
				CertificateAuthorityData: cert,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			name: {
				Cluster:  name,
				AuthInfo: name,
			},
		},
		CurrentContext: name,
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			name: {},
		},
	}

	kubeconfigRaw, err := runtime.Encode(configlatest.Codec, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("unable to encode kubeconfig: %w", err)
	}

	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigRaw)
	if err != nil {
		return nil, fmt.Errorf("unable to create rest config: %w", err)
	}

	config.AuthProvider = &clientcmdapi.AuthProviderConfig{Name: googleAuthPlugin}

	return client.New(config, client.Options{})
}

type virtualGardenClient struct {
	kubeconfig string
	token      string
	renew      time.Time
}

func getVirtualGardenClient(ctx context.Context, gardenClient client.Client) (*virtualGardenClient, error) {
	virtualGardenTokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "shoot-access-virtual-garden",
			Namespace: "garden",
		},
	}
	err := gardenClient.Get(ctx, client.ObjectKeyFromObject(virtualGardenTokenSecret), virtualGardenTokenSecret)
	if err != nil {
		return nil, fmt.Errorf("no garden token secret found for accessing virtual garden: %w", err)
	}

	renewRaw, ok := virtualGardenTokenSecret.Annotations["serviceaccount.resources.gardener.cloud/token-renew-timestamp"]
	if !ok {
		return nil, fmt.Errorf("gardener token secret has no token-renew-timestamp annotation")
	}

	renew, err := time.Parse(time.RFC3339, renewRaw)
	if err != nil {
		return nil, fmt.Errorf("unable to parse renew timestamp from gardener token secret: %w", err)
	}

	var genericSecrets corev1.SecretList
	err = gardenClient.List(context.Background(), &genericSecrets, client.MatchingLabels{
		"managed-by":       "secrets-manager",
		"manager-identity": "gardener-operator",
		"name":             "generic-token-kubeconfig",
	}, client.InNamespace("garden"))
	if err != nil {
		return nil, fmt.Errorf("unable to list secrets: %w", err)
	}

	var genericSecret *corev1.Secret
	for _, secret := range genericSecrets.Items {
		if genericSecret == nil {
			genericSecret = &secret
		} else if genericSecret.Labels["issued-at-time"] < secret.Labels["issued-at-time"] {
			genericSecret = &secret
		}
	}

	if genericSecret == nil {
		return nil, fmt.Errorf("no generic kubeconfig secret found for accessing virtual garden: %w", err)
	}

	gardenObjs := &unstructured.UnstructuredList{}
	gardenObjs.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "operator.gardener.cloud",
		Kind:    "GardenList",
		Version: "v1alpha1",
	})

	err = gardenClient.List(ctx, gardenObjs)
	if err != nil {
		return nil, fmt.Errorf("unable to list garden resources: %w", err)
	}

	if len(gardenObjs.Items) == 0 {
		return nil, fmt.Errorf("no garden resource found")
	}

	type garden struct {
		Spec struct {
			VirtualCluster struct {
				Dns struct {
					Domains []struct {
						Name string `json:"name"`
					} `json:"domains"`
				} `json:"dns"`
			} `json:"virtualCluster"`
		} `json:"spec"`
	}

	gardenRaw, err := json.Marshal(gardenObjs.Items[0].Object)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal gardener object: %w", err)
	}

	var gardenResource *garden
	err = json.Unmarshal(gardenRaw, &gardenResource)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal gardener object: %w", err)
	}

	kubeconfig := &configv1.Config{}
	err = runtime.DecodeInto(configlatest.Codec, genericSecret.Data["kubeconfig"], kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("unable to decode kubeconfig: %w", err)
	}

	kubeconfig.AuthInfos[0].AuthInfo = configv1.AuthInfo{
		TokenFile: tokenFilePath,
	}
	kubeconfig.Clusters[0].Cluster.Server = "https://api." + gardenResource.Spec.VirtualCluster.Dns.Domains[0].Name

	virtualGardenKubeconfigRaw, err := runtime.Encode(configlatest.Codec, kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("unable to encode kubeconfig: %w", err)
	}

	return &virtualGardenClient{
		kubeconfig: string(virtualGardenKubeconfigRaw),
		token:      string(virtualGardenTokenSecret.Data["token"]),
		renew:      renew,
	}, nil
}
