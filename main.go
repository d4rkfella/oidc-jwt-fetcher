package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"net/http"
	"net/url"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type TokenResponse struct {
	AccessToken string `json:"access_token"`
}

func main() {
	if err := run(); err != nil {
		printError(fmt.Sprintf("Error: %v", err))
		os.Exit(2)
	}

	fmt.Println("\033[1;32mSuccessfully managed OIDC token secret\033[0m")
}

func run() error {
	if err := validateEnv(); err != nil {
		return fmt.Errorf("environment validation failed: %w", err)
	}

	token, err := getOIDCToken()
	if err != nil {
		return fmt.Errorf("failed to retrieve OIDC token: %w", err)
	}

	namespace, err := getCurrentNamespace()
	if err != nil {
		return fmt.Errorf("failed to determine namespace: %w", err)
	}

	if err := createOrUpdateSecret(token, namespace); err != nil {
		return fmt.Errorf("secret operation failed: %w", err)
	}

	return nil
}

func getCurrentNamespace() (string, error) {
	if ns, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return string(ns), nil
	}

	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns, nil
	}

	return "default", nil
}

func validateEnv() error {
	var missing []string

	if os.Getenv("TOKEN_ENDPOINT_URL") == "" {
		missing = append(missing, "TOKEN_ENDPOINT_URL")
	}
	if os.Getenv("CLIENT_ID") == "" {
		missing = append(missing, "CLIENT_ID")
	}

	secretFile := os.Getenv("CLIENT_SECRET_FILE")
	if secretFile == "" {
		missing = append(missing, "CLIENT_SECRET_FILE")
	} else if _, err := os.Stat(secretFile); os.IsNotExist(err) {
		return fmt.Errorf("CLIENT_SECRET_FILE does not exist at %s", secretFile)
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return nil
}

func getOIDCToken() (string, error) {
	clientSecret, err := readFile(os.Getenv("CLIENT_SECRET_FILE"))
	if err != nil {
		return "", err
	}

	values := url.Values{
		"client_id":     []string{os.Getenv("CLIENT_ID")},
		"client_secret": []string{clientSecret},
		"grant_type":    []string{"client_credentials"},
	}

	if scope := os.Getenv("SCOPE"); scope != "" {
		values.Add("scope", scope)
	}

	resp, err := http.PostForm(os.Getenv("TOKEN_ENDPOINT_URL"), values)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := readResponseBody(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read token response body: %w", err)
		}
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("empty access token in response")
	}

	return tokenResp.AccessToken, nil
}

func createOrUpdateSecret(token string, namespace string) error {
	config, err := getK8sConfig()
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "oidc-jwt",
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"token": token,
		},
	}

	ctx := context.Background()
	_, err = clientset.CoreV1().Secrets(namespace).Get(ctx, "oidc-jwt", metav1.GetOptions{})

	switch {
	case err == nil:
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update secret: %w", err)
		}
		fmt.Printf("Updated existing secret in namespace %s\n", namespace)
	case errors.IsNotFound(err):
		_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create secret: %w", err)
		}
		fmt.Printf("Created new secret in namespace %s\n", namespace)
	default:
		return fmt.Errorf("failed to check secret existence: %w", err)
	}

	return nil
}

func getK8sConfig() (*rest.Config, error) {
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file at %s: %v", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func readResponseBody(body io.ReadCloser) (string, error) {
	buf := make([]byte, 512)
	n, err := body.Read(buf)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(buf[:n]), nil
}

func printError(msg string) {
	fmt.Printf("\033[1;31m%s\033[0m\n", msg)
}
