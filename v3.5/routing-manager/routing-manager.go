package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ClusterInfo ...
type ClusterInfo struct {
	ClusterName       string
	PodName           string
	CreatedAt         time.Time
	HasSoftConstraint bool
}

// UserClusterAssociation ...
type UserClusterAssociation struct {
	Data map[string]map[string]*ClusterInfo //userID -> appName -> cluster measure
	mu   sync.RWMutex
}

// UpdateAssociations ...
func (u *UserClusterAssociation) UpdateAssociations(associations map[string]map[string]*ClusterInfo) {
	log.Println("Updating associations")
	u.mu.Lock()
	defer u.mu.Unlock()

	u.Data = associations
}

// handleUpdateAssociations ...
func (u *UserClusterAssociation) handleUpdateAssociations(w http.ResponseWriter, r *http.Request) {
	log.Println("Handling update associations request")
	var associations map[string]map[string]*ClusterInfo
	if err := json.NewDecoder(r.Body).Decode(&associations); err != nil {
		log.Printf("Error decoding request body: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	u.UpdateAssociations(associations)
	w.WriteHeader(http.StatusOK)
}

// getClusterInfoForUser ...
func (u *UserClusterAssociation) getClusterInfoForUser(user string, appName string) (*ClusterInfo, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()

	clusterInfo, exists := u.Data[user][appName]
	return clusterInfo, exists
}

type KubernetesService struct {
	clientset *kubernetes.Clientset
}

func NewKubernetesService() (*KubernetesService, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return &KubernetesService{clientset: clientset}, nil
}

func (ks *KubernetesService) GetPodIP(podName string) (string, error) {
	pod, err := ks.clientset.CoreV1().Pods("default").Get(context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return pod.Status.PodIP, nil
}

func (ks *KubernetesService) GetRandomPodIP(appName string) (string, error) {
	pods, err := ks.clientset.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=" + appName, // Assicurati che questo selettore sia corretto per la tua configurazione
	})
	if err != nil {
		return "", err
	}

	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found for the app %s", appName)
	}

	// Seleziona un pod casuale dalla lista
	randomIndex := rand.Intn(len(pods.Items))
	randomPod := pods.Items[randomIndex]

	if randomPod.Status.PodIP == "" {
		return "", fmt.Errorf("selected pod has no IP")
	}

	return randomPod.Status.PodIP, nil
}

// RoutingManager ...
type RoutingManager struct {
	userClusterAssociations *UserClusterAssociation
	kubernetesService       *KubernetesService
	appName                 string
}

// ServeHTTP ...
func (rm *RoutingManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Println("Received a new request")
	// Extract the user ID from the query parameter
	queryValues := r.URL.Query()
	userID := queryValues.Get("id")
	if userID == "" {
		log.Println("User ID not provided in the request")
		http.Error(w, "User ID not provided", http.StatusBadRequest)
		return
	}

	log.Printf("Looking up cluster info for user ID: %s", userID)
	// Get the cluster info based on the user ID
	clusterInfo, exists := rm.userClusterAssociations.getClusterInfoForUser(userID, rm.appName)
	var target string
	if exists {
		log.Printf("User ID %s is associated with cluster info: %+v", userID, clusterInfo)
		podIP, err := rm.kubernetesService.GetPodIP(clusterInfo.PodName)
		if err != nil {
			log.Printf("Failed to get IP for pod %s: %v", clusterInfo.PodName, err)
			log.Printf("Using default service...")
			podIP, err := rm.kubernetesService.GetRandomPodIP(rm.appName)
			if err != nil {
				log.Printf("Failed to get a random pod IP: %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			target = fmt.Sprintf("%s:8080", podIP)
		}
		target = fmt.Sprintf("%s:8080", podIP)
	} else {
		log.Printf("No cluster info found for user ID %s, using default service", userID)
		podIP, err := rm.kubernetesService.GetRandomPodIP(rm.appName)
		if err != nil {
			log.Printf("Failed to get a random pod IP: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		target = fmt.Sprintf("%s:8080", podIP)
	}

	log.Printf("Proxying request to target: %s", target)

	// Create a reverse proxy to forward the request
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: target})

	// Update the request with the destination host
	r.URL.Host = target
	r.URL.Scheme = "http"
	r.Header.Set("X-Forwarded-Host", r.Header.Get("Host"))
	r.Host = target

	// Forward the request to the corresponding pod or service
	proxy.ServeHTTP(w, r)
	log.Println("Request has been proxied to target")
}

func main() {
	// rand.Seed(time.Now().UnixNano()) // deprecated
	log.Println("Starting server...")

	// Initialize Kubernetes service
	kubernetesService, err := NewKubernetesService()
	if err != nil {
		log.Fatalf("Failed to initialize Kubernetes service: %v", err)
	}

	// Read the environment variable for the default service
	/*
		defaultService, exists := os.LookupEnv("DEFAULT_SERVICE")
		if !exists || defaultService == "" {
			log.Fatal("DEFAULT_SERVICE environment variable not set or empty")
		}
	*/

	// Read the environment variable for the application name
	appName, exists := os.LookupEnv("APP_NAME")
	if !exists || appName == "" {
		log.Fatal("APP_NAME environment variable not set or empty")
	}

	userClusterAssociations := &UserClusterAssociation{
		Data: make(map[string]map[string]*ClusterInfo),
	}

	rm := &RoutingManager{
		userClusterAssociations: userClusterAssociations,
		kubernetesService:       kubernetesService,
		appName:                 appName, // Set the default service
	}

	router := mux.NewRouter()
	router.HandleFunc("/update-associations", userClusterAssociations.handleUpdateAssociations).Methods("POST")
	router.PathPrefix("/").Handler(rm) // Catch-all route for incoming traffic to be managed

	headers := handlers.AllowedHeaders([]string{"X-Requested-With", "Content-Type", "Authorization", "X-User-Header"})
	methods := handlers.AllowedMethods([]string{"GET", "POST", "PUT", "DELETE", "OPTIONS"})
	origins := handlers.AllowedOrigins([]string{"*"})

	log.Println("Server is ready to handle requests at port 80")
	http.ListenAndServe(":80", handlers.CORS(headers, methods, origins)(router))
}
