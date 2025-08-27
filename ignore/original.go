package webhooksandbox
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/sirupsen/logrus"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Global variables
var (
	clientset *kubernetes.Clientset
	initOnce  sync.Once // Ensures that the client is initialized only once.
)

// GetKubeClient provides a thread-safe singleton instance of the Kubernetes client.
func GetKubeClient() (*kubernetes.Clientset, error) {
	var err error
	initOnce.Do(func() {
		clientset, err = initializeClient()
	})
	return clientset, err
}

// initializeClient initializes the Kubernetes client.
func initializeClient() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		logrus.Warn("Falling back to kubeconfig: ", err)
		kubeconfig := getKubeConfigPath()
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	logrus.Info("Kubernetes client initialized successfully")
	return clientset, nil
}

// getKubeConfigPath determines the kubeconfig path.
func getKubeConfigPath() string {
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		return kubeconfig
	}
	return filepath.Join(homeDir(), ".kube", "config")
}

// homeDir returns the user’s home directory.
func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE")
}

func main() {
	setLogger()

	http.HandleFunc("/add-label", addLabelHook) // Webhook endpoint.
	http.HandleFunc("/health", serveHealth)     // Health check endpoint.

	port := getPort()
	if os.Getenv("TLS") == "true" {
		logrus.Infof("Starting server on port %s with TLS", port)
		logrus.Fatal(http.ListenAndServeTLS(port,
			"/etc/mutating-webhook/tls/tls.crt",
			"/etc/mutating-webhook/tls/tls.key", nil))
	} else {
		logrus.Infof("Starting server on port %s", port)
		logrus.Fatal(http.ListenAndServe(port, nil))
	}
}

// getPort retrieves the port or defaults to ":8080".
func getPort() string {
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}

// serveHealth provides a basic health check endpoint.
func serveHealth(w http.ResponseWriter, r *http.Request) {
	logrus.WithField("uri", r.RequestURI).Debug("Health check OK")
	fmt.Fprint(w, "OK")
}

// addLabelHook processes AdmissionReview requests and adds the "team" label if applicable.
func addLabelHook(w http.ResponseWriter, r *http.Request) {
	logger := logrus.WithField("uri", r.RequestURI)

	in, err := parseRequest(r)
	if err != nil {
		logger.Error(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out, err := addLabel(*in)

	var admissionReviewResponse admissionv1.AdmissionReview
	admissionReviewResponse.Response = out

	admissionReviewResponse.SetGroupVersionKind(in.GroupVersionKind())

	if err != nil {
		logger.Error(fmt.Sprintf("could not generate admission response: %v", err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	jout, _ := json.Marshal(admissionReviewResponse)
	logger.Debugf("AdmissionResponse: %s", jout)
	fmt.Fprintf(w, "%s", jout)
}

// addLabel adds the "team" label if the namespace has it; otherwise, allows the request unmodified.
func addLabel(ar admissionv1.AdmissionReview) (*admissionv1.AdmissionResponse, error) {
	// Get the Kubernetes client.
	c, err := GetKubeClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes client: %w", err)
	}

	// Convert the raw JSON to an unstructured Kubernetes object.
	var obj runtime.Object
	var scope conversion.Scope
	err = runtime.Convert_runtime_RawExtension_To_runtime_Object(&ar.Request.Object, &obj, scope)
	if err != nil {
		return nil, fmt.Errorf("failed to convert object: %w", err)
	}

  
	innerObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
    return nil, fmt.Errorf("failed to convert to unstructured: %w", err)
	}

	u := unstructured.Unstructured{Object: innerObj}

  podLabels := u.GetLabels()
  fmt.Println("pod labels =>", podLabels)

	// Fetch the namespace and check for the "team" label.
	namespaceName := ar.Request.Namespace
	namespace, err := c.CoreV1().Namespaces().Get(context.TODO(), namespaceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch namespace %s: %w", namespaceName, err)
	}

	// Check if the namespace has the "team" label.
	teamLabelValue, exists := namespace.Labels["team"]
	if !exists {
		// If the "team" label is not present, allow the request without modification.
		return &admissionv1.AdmissionResponse{Allowed: true, UID: ar.Request.UID}, nil
	}

	// Prepare the response with a patch if the label needs to be added.
	reviewResponse := &admissionv1.AdmissionResponse{Allowed: true, UID: ar.Request.UID}
	pt := admissionv1.PatchTypeJSONPatch

	// If the team label is not set, patch it.
	switch {
	case u.GetLabels() == nil:
		patch := []byte(`[
            {
                "op": "add",
                "path": "/metadata/labels",
                "value": {}
            },
            {
                "op": "add",
                "path": "/metadata/labels/team",
                "value": "` + teamLabelValue + `"
            }
        ]`)
		reviewResponse.Patch = patch
		reviewResponse.PatchType = &pt

	case !hasLabel(u.GetLabels(), "team"):
		patch := []byte(`[
            {
                "op": "add",
                "path": "/metadata/labels/team",
                "value": "` + teamLabelValue + `"
            }
        ]`)
		reviewResponse.Patch = patch
		reviewResponse.PatchType = &pt
	}

	return reviewResponse, nil
}

// hasLabel checks if a specific label exists in the provided map.
func hasLabel(labels map[string]string, key string) bool {
	_, exists := labels[key]
	return exists
}

// parseRequest extracts an AdmissionReview from the HTTP request.
func parseRequest(r *http.Request) (*admissionv1.AdmissionReview, error) {
	if r.Header.Get("Content-Type") != "application/json" {
		return nil, fmt.Errorf("invalid Content-Type, expected application/json")
	}

	var ar admissionv1.AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&ar); err != nil {
		return nil, fmt.Errorf("failed to decode admission review: %w", err)
	}
	if ar.Request == nil {
		return nil, fmt.Errorf("invalid admission review: missing request field")
	}
	return &ar, nil
}

// setLogger configures logrus based on environment variables.
func setLogger() {
	logrus.SetLevel(logrus.DebugLevel)
	if lev := os.Getenv("LOG_LEVEL"); lev != "" {
		if parsedLevel, err := logrus.ParseLevel(lev); err == nil {
			logrus.SetLevel(parsedLevel)
		}
	}
	if os.Getenv("LOG_JSON") == "true" {
		logrus.SetFormatter(&logrus.JSONFormatter{})
	}
}
