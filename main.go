package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	httpTimeout         = 15 * time.Second
	k8sOperationTimeout = 10 * time.Second
	maxRetries          = 3
	baseRetryDelay      = 2 * time.Second
	secretName          = "oidc-jwt"
	appName             = "oidc-secret-manager"
)

type TokenResponse struct {
	AccessToken string `json:"access_token"`
}

func initLogger() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	output := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	log.Logger = log.Output(output).With().Str("app", appName).Logger()
}

func main() {
	initLogger()

	if err := run(); err != nil {
		log.Error().Err(err).Msg("Application failed")
		os.Exit(1)
	}
	log.Info().Msg("Successfully managed OIDC token secrets across all namespaces")
}

func run() error {
	log.Info().Msg("Starting OIDC secret management")

	if err := validateEnv(); err != nil {
		return fmt.Errorf("environment validation failed: %w", err)
	}

	token, err := getOIDCToken()
	if err != nil {
		return fmt.Errorf("failed to retrieve OIDC token: %w", err)
	}
	log.Info().Msg("Successfully obtained OIDC token")

	if err := manageSecretsClusterWide(token); err != nil {
		return fmt.Errorf("secret management failed: %w", err)
	}

	return nil
}

func validateEnv() error {
	var missing []string
	required := []string{"TOKEN_ENDPOINT_URL", "CLIENT_ID", "CLIENT_SECRET_FILE"}

	for _, env := range required {
		if os.Getenv(env) == "" {
			missing = append(missing, env)
		}
	}

	if secretFile := os.Getenv("CLIENT_SECRET_FILE"); secretFile != "" {
		if _, err := os.Stat(secretFile); os.IsNotExist(err) {
			return fmt.Errorf("CLIENT_SECRET_FILE does not exist at %s", secretFile)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return nil
}

func getOIDCToken() (string, error) {
	clientSecret, err := readFile(os.Getenv("CLIENT_SECRET_FILE"))
	if err != nil {
		return "", fmt.Errorf("failed to read client secret: %w", err)
	}

	values := url.Values{
		"client_id":     {os.Getenv("CLIENT_ID")},
		"client_secret": {clientSecret},
		"grant_type":    {"client_credentials"},
	}

	if scope := os.Getenv("SCOPE"); scope != "" {
		values.Add("scope", scope)
	}

	client := &http.Client{Timeout: httpTimeout}
	var resp *http.Response
	var lastError error

	for i := 0; i < maxRetries; i++ {
		log.Debug().
			Str("attempt", fmt.Sprintf("%d/%d", i+1, maxRetries)).
			Str("endpoint", os.Getenv("TOKEN_ENDPOINT_URL")).
			Msg("Attempting to fetch OIDC token")

		resp, lastError = client.PostForm(os.Getenv("TOKEN_ENDPOINT_URL"), values)
		if lastError == nil && resp.StatusCode == http.StatusOK {
			break
		}

		if lastError != nil {
			log.Warn().Err(lastError).Msg("Token request failed")
		} else {
			body, _ := readResponseBody(resp.Body)
			log.Warn().
				Int("status_code", resp.StatusCode).
				Str("response", body).
				Msg("Token endpoint returned non-200 status")
			resp.Body.Close()
		}

		if i < maxRetries-1 {
			delay := time.Duration(i+1) * baseRetryDelay
			log.Info().Dur("retry_delay", delay).Msg("Retrying after delay")
			time.Sleep(delay)
		}
	}

	if lastError != nil {
		return "", fmt.Errorf("token request failed after %d attempts: %w", maxRetries, lastError)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readResponseBody(resp.Body)
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", errors.New("empty access token in response")
	}

	return tokenResp.AccessToken, nil
}

func manageSecretsClusterWide(token string) error {
	config, err := getK8sConfig()
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	namespaces, err := listNamespaces(clientset)
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	var errorList []error
	successCount := 0

	for _, ns := range namespaces {
		logger := log.With().Str("namespace", ns).Logger()

		if err := withRetries(func() error {
			return manageNamespaceSecret(clientset, token, ns, logger)
		}); err != nil {
			errorList = append(errorList, fmt.Errorf("namespace %s: %w", ns, err))
			logger.Error().Err(err).Msg("Failed to manage secret")
		} else {
			successCount++
		}
	}

	log.Info().
		Int("success_count", successCount).
		Int("error_count", len(errorList)).
		Int("total_namespaces", len(namespaces)).
		Msg("Secret management completed")

	if len(errorList) > 0 {
		return fmt.Errorf("completed with %d errors: %v", len(errorList), errorList)
	}
	return nil
}

func listNamespaces(clientset *kubernetes.Clientset) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), k8sOperationTimeout)
	defer cancel()

	log.Debug().Msg("Listing all namespaces")
	nsList, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	namespaces := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		namespaces = append(namespaces, ns.Name)
	}

	log.Debug().Int("count", len(namespaces)).Msg("Discovered namespaces")
	return namespaces, nil
}

func manageNamespaceSecret(clientset *kubernetes.Clientset, token, namespace string, logger zerolog.Logger) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": appName,
				"app.kubernetes.io/created-by": appName,
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"token": token,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), k8sOperationTimeout)
	defer cancel()

	existing, err := clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	switch {
	case k8serrors.IsNotFound(err):
		logger.Info().Msg("Creating new secret")
		_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
		if err == nil {
			logger.Info().Msg("Successfully created secret")
		}
	case err == nil:
		if existing.StringData["token"] == token {
			logger.Debug().Msg("Token unchanged - no update needed")
			return nil
		}
		logger.Info().Msg("Updating existing secret")
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err == nil {
			logger.Info().Msg("Successfully updated secret")
		}
	}
	return err
}

func withRetries(fn func() error) error {
	var lastError error

	for i := 0; i < maxRetries; i++ {
		lastError = fn()
		if lastError == nil {
			return nil
		}

		if i < maxRetries-1 {
			delay := time.Duration(i+1) * baseRetryDelay
			log.Warn().
				Err(lastError).
				Str("attempt", fmt.Sprintf("%d/%d", i+1, maxRetries)).
				Dur("retry_delay", delay).
				Msg("Operation failed, retrying")
			time.Sleep(delay)
		}
	}

	return fmt.Errorf("failed after %d attempts: %w", maxRetries, lastError)
}

func getK8sConfig() (*rest.Config, error) {
	if config, err := rest.InClusterConfig(); err == nil {
		log.Debug().Msg("Using in-cluster Kubernetes configuration")
		return config, nil
	}

	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	log.Debug().Str("path", kubeconfig).Msg("Using kubeconfig file")
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func readResponseBody(body io.ReadCloser) (string, error) {
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	return string(data), nil
}
