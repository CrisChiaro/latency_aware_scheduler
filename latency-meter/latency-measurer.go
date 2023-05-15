package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var appAddress string = "http://localhost:8081"

type LatencyMeasurement struct {
	NodeName    string
	Measurement int64
	Timestamp   time.Time
}

type LatencyMeasurements struct {
	data map[string]*LatencyMeasurement
}

func NewLatencyMeasurements() *LatencyMeasurements {
	return &LatencyMeasurements{
		data: make(map[string]*LatencyMeasurement),
	}
}

func (l *LatencyMeasurements) AddLatency(podName string, measurement *LatencyMeasurement) {
	l.data[podName] = measurement
}

func latencyMiddleware(next http.Handler, pod *v1.Pod, latencyMeasurements *LatencyMeasurements) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		latency := time.Since(start).Milliseconds()

		latencyMeasurements.AddLatency(pod.Name, &LatencyMeasurement{
			NodeName:    pod.Spec.NodeName,
			Measurement: latency,
			Timestamp:   time.Now(),
		})

		// Proxy the request to the application
		proxyURL, _ := url.Parse(appAddress)
		proxy := httputil.NewSingleHostReverseProxy(proxyURL)
		proxy.ServeHTTP(w, r)
	})
}

func getCurrentPod(clientset *kubernetes.Clientset) (*v1.Pod, error) {
	namespace := os.Getenv("POD_NAMESPACE")
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		return nil, fmt.Errorf("Error getting current pod: POD_NAME environment variable is not set")
	}
	if namespace == "" {
		return nil, fmt.Errorf("Error getting current pod: POD_NAMESPACE environment variable is not set")
	}

	pod, err := clientset.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Error getting current pod: %v", err)
	}

	return pod, nil
}

func measureLatency(clientset *kubernetes.Clientset, latencyMeasurements *LatencyMeasurements) {
	pod, err := getCurrentPod(clientset)
	if err != nil {
		fmt.Printf("Error getting current pod: %v\n", err)
		return
	}

	mux := http.NewServeMux()

	mux.Handle("/app", latencyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Proxy the request to the application
		proxyURL, _ := url.Parse(appAddress)
		proxy := httputil.NewSingleHostReverseProxy(proxyURL)
		proxy.ServeHTTP(w, r)
	}), pod, latencyMeasurements))

	mux.HandleFunc("/measurements", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		latencyMeasurementsJSON, err := json.Marshal(latencyMeasurements.data)
		if err != nil {
			http.Error(w, "Error marshaling latency measurements", http.StatusInternalServerError)
			return
		}

		w.Write(latencyMeasurementsJSON)
	})

	http.ListenAndServe(":8080", mux)
}

func main() {
	fmt.Println("Latency Measurer started")

	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			fmt.Println("Error in getting Kubernetes configuration:", err)
			return
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Println("Error in creating Kubernetes clientset:", err)
		return
	}

	latencyMeasurements := NewLatencyMeasurements()
	go measureLatency(clientset, latencyMeasurements)

	select {}
}
