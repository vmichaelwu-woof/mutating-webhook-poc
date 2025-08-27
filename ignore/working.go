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
// addLabel adds a "service" label to Pods if car_id/appName labels are present.
func addLabel(ar admissionv1.AdmissionReview) (*admissionv1.AdmissionResponse, error) {
	// Get the Kubernetes client.
	c, err := GetKubeClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes client: %w", err)
	}

	// Convert the raw JSON to an unstructured Kubernetes object.
	var obj runtime.Object
	var scope conversion.Scope
	if err := runtime.Convert_runtime_RawExtension_To_runtime_Object(&ar.Request.Object, &obj, scope); err != nil {
		return nil, fmt.Errorf("failed to convert object: %w", err)
	}

	innerObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to unstructured: %w", err)
	}
	u := unstructured.Unstructured{Object: innerObj}

	// Only handle Pods; otherwise allow as-is.
	if u.GetKind() != "Pod" {
		return &admissionv1.AdmissionResponse{Allowed: true, UID: ar.Request.UID}, nil
	}

	logrus.WithFields(logrus.Fields{
		"kind":   u.GetKind(),
		"name":   u.GetName(),
		"labels": u.GetLabels(),
	}).Debug("Incoming object labels")

	// Fetch namespace (still verifying namespace exists)
	namespaceName := ar.Request.Namespace
	if namespaceName == "" {
		namespaceName = u.GetNamespace()
	}
	if namespaceName == "" {
		namespaceName = "default"
	}
	if _, err := c.CoreV1().Namespaces().Get(context.TODO(), namespaceName, metav1.GetOptions{}); err != nil {
		return nil, fmt.Errorf("failed to fetch namespace %s: %w", namespaceName, err)
	}

	// Build desired "service" value from existing pod labels
	podLabels := u.GetLabels()
	carID, hasCar := podLabels["car_id"]
	appName, hasApp := podLabels["appName"]

	var serviceVal string
	switch {
	case hasApp && hasCar:
		serviceVal = fmt.Sprintf("%s-%s", appName, carID)
	case hasApp:
		serviceVal = appName
	case hasCar:
		serviceVal = carID
	default:
		// Nothing to derive → allow without changes
		return &admissionv1.AdmissionResponse{Allowed: true, UID: ar.Request.UID}, nil
	}

	// Target label key
	serviceLabelKey := "service"
	serviceLabelPath := "/metadata/labels/service"

	// If label already present with same value → no-op
	if val, ok := podLabels[serviceLabelKey]; ok && val == serviceVal {
		return &admissionv1.AdmissionResponse{Allowed: true, UID: ar.Request.UID}, nil
	}

	reviewResponse := &admissionv1.AdmissionResponse{Allowed: true, UID: ar.Request.UID}
	pt := admissionv1.PatchTypeJSONPatch

	// Build patch depending on whether labels map exists and whether key exists
	switch {
	case podLabels == nil:
		// Add the labels map, then add our key
		patch := []byte(`[
		  { "op": "add", "path": "/metadata/labels", "value": {} },
		  { "op": "add", "path": "` + serviceLabelPath + `", "value": "` + serviceVal + `" }
		]`)
		reviewResponse.Patch = patch
		reviewResponse.PatchType = &pt

	case !hasLabel(podLabels, serviceLabelKey):
		// Labels exist, but key does not: add it
		patch := []byte(`[
		  { "op": "add", "path": "` + serviceLabelPath + `", "value": "` + serviceVal + `" }
		]`)
		reviewResponse.Patch = patch
		reviewResponse.PatchType = &pt

	default:
		// Key exists but value differs: replace it
		patch := []byte(`[
		  { "op": "replace", "path": "` + serviceLabelPath + `", "value": "` + serviceVal + `" }
		]`)
		reviewResponse.Patch = patch
		reviewResponse.PatchType = &pt
	}

	logrus.WithFields(logrus.Fields{
		"targetLabel": serviceLabelKey,
		"value":       serviceVal,
		"pod":         u.GetName(),
		"namespace":   namespaceName,
	}).Debug("Patching service label")

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
