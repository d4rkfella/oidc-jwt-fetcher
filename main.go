package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	// Autoload GKE auth plugin
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

const (
	defaultScopes           = "openid"
	defaultSecretName       = "oidc-token-secret"
	defaultSecretKey        = "token"
	defaultTokenTimeout     = 30 * time.Second
	k8sListNamespaceTimeout = 1 * time.Minute
	k8sSecretOpTimeout      = 30 * time.Second
	TargetNamespacesEnvVar  = "TARGET_NAMESPACES"
)

type OIDCTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

func main() {
	log.Println("Starting OIDC JWT Fetcher CronJob...")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Println("Shutdown signal received, context cancelled. Attempting to exit gracefully...")
	}()

	tokenURL := getEnvOrDie("OIDC_TOKEN_URL")
	clientID := getEnvOrDie("OIDC_CLIENT_ID")
	clientSecret := getEnvOrDie("OIDC_CLIENT_SECRET")
	scopes := getEnv("OIDC_SCOPES", defaultScopes)
	k8sSecretName := getEnv("K8S_SECRET_NAME", defaultSecretName)
	k8sSecretKey := getEnv("K8S_SECRET_KEY", defaultSecretKey)

	log.Println("Fetching OIDC token...")
	accessToken, err := fetchOIDCToken(tokenURL, clientID, clientSecret, scopes)
	if err != nil {
		log.Fatalf("Error fetching OIDC token: %v", err)
	}
	log.Println("Successfully fetched OIDC token.")

	log.Println("Initializing Kubernetes client...")
	kubeClient, err := getKubeClient()
	if err != nil {
		log.Fatalf("Error initializing Kubernetes client: %v", err)
	}
	log.Println("Successfully initialized Kubernetes client.")

	var namespacesToProcess []string
	targetNamespacesStr := os.Getenv(TargetNamespacesEnvVar)

	if targetNamespacesStr != "" {
		log.Printf("TARGET_NAMESPACES is set: '%s'. Processing only these namespaces.", targetNamespacesStr)
		namespacesToProcess = strings.Split(targetNamespacesStr, ",")
		for i, ns := range namespacesToProcess {
			namespacesToProcess[i] = strings.TrimSpace(ns)
		}
		var nonEmptyNamespaces []string
		for _, ns := range namespacesToProcess {
			if ns != "" {
				nonEmptyNamespaces = append(nonEmptyNamespaces, ns)
			}
		}
		namespacesToProcess = nonEmptyNamespaces
		if len(namespacesToProcess) == 0 {
			log.Println("TARGET_NAMESPACES was set but resulted in an empty list after parsing. No namespaces to process.")
		}
	} else {
		log.Println("TARGET_NAMESPACES is not set or is empty. Attempting to list all namespaces in the cluster.")
		listCtx, listCancel := context.WithTimeout(ctx, k8sListNamespaceTimeout)
		defer listCancel()
		namespacesFromCluster, listErr := listNamespaces(listCtx, kubeClient)
		if listErr != nil {
			if listCtx.Err() == context.DeadlineExceeded {
				log.Fatalf("Error listing all namespaces: timeout after %v: %v", k8sListNamespaceTimeout, listErr)
			} else if ctx.Err() == context.Canceled {
				log.Printf("Shutdown signal received, namespace listing interrupted.")
				return
			}
			log.Fatalf("Error listing all namespaces: %v", listErr)
		}
		listCancel()
		namespacesToProcess = namespacesFromCluster
	}

	if len(namespacesToProcess) == 0 {
		log.Println("No namespaces identified for processing. Exiting.")
		return
	}
	log.Printf("Found %d namespaces to process: %v", len(namespacesToProcess), namespacesToProcess)

	if err := processSecretsInNamespaces(ctx, kubeClient, namespacesToProcess, k8sSecretName, k8sSecretKey, accessToken); err != nil {
		log.Printf("Processing namespaces finished with error/signal: %v", err)
		return
	}

	log.Println("OIDC JWT Fetcher CronJob finished successfully.")
}

func getEnvOrDie(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("Environment variable %s not set", key)
	}
	return value
}

func getEnv(key, defaultValue string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return defaultValue
	}
	return value
}

func fetchOIDCToken(tokenURL, clientID, clientSecret, scopes string) (accessToken string, err error) {
	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	data.Set("scope", scopes)

	client := &http.Client{Timeout: defaultTokenTimeout}
	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			if err == nil {
				err = fmt.Errorf("failed to close response body: %w", closeErr)
			} else {
				log.Printf("Warning: failed to close response body: %v", closeErr)
			}
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch token, status code: %d", resp.StatusCode)
	}

	var tokenResponse OIDCTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResponse); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	if tokenResponse.AccessToken == "" {
		return "", fmt.Errorf("access token not found in response")
	}

	return tokenResponse.AccessToken, nil
}

func getKubeClient() (kubernetes.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Println("Not in cluster, attempting to use local kubeconfig")
		return nil, fmt.Errorf("failed to get in-cluster config: %w. For local dev, ensure KUBECONFIG is set or run within a cluster", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}
	return clientset, nil
}

func listNamespaces(ctx context.Context, clientset kubernetes.Interface) ([]string, error) {
	namespaceList, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	names := make([]string, 0, len(namespaceList.Items))
	for _, ns := range namespaceList.Items {
		names = append(names, ns.Name)
	}
	return names, nil
}

func createOrUpdateSecret(ctx context.Context, clientset kubernetes.Interface, namespace, secretName, secretKey, token string) error {
	secretClient := clientset.CoreV1().Secrets(namespace)

	_, err := secretClient.Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			log.Printf("Secret '%s' not found in namespace '%s'. Creating...", secretName, namespace)
			secretData := map[string][]byte{
				secretKey: []byte(token),
			}
			newSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				Data: secretData,
				Type: corev1.SecretTypeOpaque,
			}
			_, createErr := secretClient.Create(ctx, newSecret, metav1.CreateOptions{})
			if createErr != nil {
				return fmt.Errorf("failed to create secret '%s' in namespace '%s': %w", secretName, namespace, createErr)
			}
			return nil
		} else {
			return fmt.Errorf("failed to get secret '%s' in namespace '%s': %w", secretName, namespace, err)
		}
	}

	log.Printf("Secret '%s' found in namespace '%s'. Patching...", secretName, namespace)

	patchPayload := map[string]interface{}{
		"data": map[string]string{
			secretKey: base64.StdEncoding.EncodeToString([]byte(token)),
		},
	}
	patchBytes, marshalErr := json.Marshal(patchPayload)
	if marshalErr != nil {
		return fmt.Errorf("failed to marshal patch payload for secret '%s' in namespace '%s': %w", secretName, namespace, marshalErr)
	}

	_, patchErr := secretClient.Patch(ctx, secretName, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if patchErr != nil {
		return fmt.Errorf("failed to patch secret '%s' in namespace '%s': %w", secretName, namespace, patchErr)
	}

	return nil
}

func processSecretsInNamespaces(ctx context.Context, kubeClient kubernetes.Interface, namespaces []string, secretName, secretKey, accessToken string) error {
	for _, ns := range namespaces {
		select {
		case <-ctx.Done():
			log.Printf("Shutdown signal received, stopping further secret operations.")
			return ctx.Err()
		default:
		}

		log.Printf("Processing namespace: %s", ns)
		secretOpCtx, secretOpCancel := context.WithTimeout(ctx, k8sSecretOpTimeout)

		err := createOrUpdateSecret(secretOpCtx, kubeClient, ns, secretName, secretKey, accessToken)

		if err != nil {
			secretOpCancel()
			if secretOpCtx.Err() == context.DeadlineExceeded {
				log.Fatalf("Error creating/updating secret in namespace %s: timeout after %v: %v", ns, k8sSecretOpTimeout, err)
			} else if ctx.Err() == context.Canceled {
				log.Printf("Shutdown signal received, secret operation in namespace %s interrupted.", ns)
				return ctx.Err()
			}
			log.Fatalf("Error creating/updating secret in namespace %s: %v", ns, err)
		}
		secretOpCancel()
		log.Printf("Successfully created/updated secret '%s' in namespace '%s'", secretName, ns)
	}
	return nil
}
