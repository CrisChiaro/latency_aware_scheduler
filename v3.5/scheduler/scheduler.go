package main

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type CustomScheduler struct {
	clientset             *kubernetes.Clientset
	mutex                 *sync.Mutex
	queue                 workqueue.RateLimitingInterface
	informer              cache.SharedIndexInformer
	visitedNodesPerApp    map[string]map[string]bool
	hardLatencyThresholds *LatencyThresholds
	softLatencyThresholds *LatencyThresholds
}

func NewCustomScheduler(clientset *kubernetes.Clientset, mutex *sync.Mutex, hardLatencyThresholds, softLatencyThresholds *LatencyThresholds) *CustomScheduler {
	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				options.FieldSelector = "spec.schedulerName=latency-aware-scheduler"
				return clientset.CoreV1().Pods(v1.NamespaceAll).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				options.FieldSelector = "spec.schedulerName=latency-aware-scheduler"
				return clientset.CoreV1().Pods(v1.NamespaceAll).Watch(context.TODO(), options)
			},
		},
		&v1.Pod{},
		0,
		cache.Indexers{},
	)

	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
	})

	return &CustomScheduler{
		clientset:             clientset,
		mutex:                 mutex,
		informer:              informer,
		queue:                 queue,
		visitedNodesPerApp:    make(map[string]map[string]bool),
		hardLatencyThresholds: hardLatencyThresholds,
		softLatencyThresholds: softLatencyThresholds,
	}
}

func (s *CustomScheduler) Run() {
	rand.Seed(time.Now().UnixNano())
	go s.informer.Run(make(chan struct{}))
	// Attendi che l'informer sia sincronizzato
	if !cache.WaitForCacheSync(make(chan struct{}), s.informer.HasSynced) {
		panic(fmt.Errorf("timeout waiting for cache sync"))
	}

	// Processa i pod in attesa di pianificazione
	for {
		key, shutdown := s.queue.Get()
		if shutdown {
			break
		}

		func() {
			defer s.queue.Done(key)
			fmt.Printf("Processing pod: %s\n", key) //debug
			err := s.schedulePod(key.(string))
			if err != nil {
				fmt.Printf("Error scheduling pod: %v\n", err)
				s.queue.AddRateLimited(key)
			} else {
				s.queue.Forget(key)
			}
		}()
	}
}

func (s *CustomScheduler) schedulePod(key string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// Ottieni il pod associato alla chiave
	pod, exists, err := s.informer.GetStore().GetByKey(key)
	if err != nil {
		return err
	}

	if !exists {
		return nil
	}

	// Scegli un nodo sul quale pianificare il pod
	node, err := s.chooseNodeForPod(pod.(*v1.Pod))
	if err != nil {
		return err
	}

	// Assegna il pod al nodo scelto
	err = s.assignPodToNode(pod.(*v1.Pod), node)

	// Aggiorna le informazioni sul nodo nel tuo elenco di nodi
	updatedNode, err := s.clientset.CoreV1().Nodes().Get(context.TODO(), node.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	*node = *updatedNode
	return err
}

func (s *CustomScheduler) chooseNodeForPod(pod *v1.Pod) (*v1.Node, error) {
	nodes, err := s.clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	nodesToConsider := nodes.Items

	if len(nodesToConsider) == 0 {
		return nil, fmt.Errorf("no nodes available")
	}

	selectedNode, err := s.getBestNode(pod, nodesToConsider)
	fmt.Println("BESTNODE: ", selectedNode.Name) //DEBUG
	return selectedNode, err
}

func (s *CustomScheduler) getBestNode(pod *v1.Pod, nodes []v1.Node) (*v1.Node, error) {
	var bestNode *v1.Node
	var bestScore float64

	appName, ok := pod.Labels["app"]
	if !ok {
		return nil, fmt.Errorf("unable to determine app name from pod labels")
	}
	hardlatencyThreshold, softLatencyThreshold, err := s.getLatencyThreshold(pod)
	if err != nil {
		return nil, err
	}
	_, exists := s.hardLatencyThresholds.GetLatency(appName)
	if !exists && hardlatencyThreshold != -1 { //se non esisteva l'hard constraint e ne ho trovato uno
		s.hardLatencyThresholds.SetLatency(appName, hardlatencyThreshold)
	}
	_, exists = s.softLatencyThresholds.GetLatency(appName)
	if !exists && softLatencyThreshold != -1 { //se non esisteva il soft constraint e ne ho trovato uno
		s.softLatencyThresholds.SetLatency(appName, softLatencyThreshold)
	}
	//DEBUG
	fmt.Println("\nAppName: ", appName)
	fmt.Println("VisitedNodes for the App: ", s.visitedNodesPerApp[appName])
	hLatency, hExists := s.hardLatencyThresholds.GetLatency(appName)
	sLatency, sExists := s.softLatencyThresholds.GetLatency(appName)
	fmt.Println("Latency Threshold:\tHard (exists: ", hExists, "): ", hLatency, "\tSoft (exists: ", sExists, "): ", sLatency)

	for i, node := range nodes {
		fmt.Println()                           //DEBUG
		fmt.Println("Node ", i, ":", node.Name) //DEBUG
		// Ignora il nodo di controllo
		if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
			//DEBUG
			fmt.Println("Control Plane Ignorato")
			continue
		}
		visitedNodes, ok := s.visitedNodesPerApp[appName]
		if !ok {
			visitedNodes = make(map[string]bool)
			s.visitedNodesPerApp[appName] = visitedNodes
		}
		_, visited := visitedNodes[node.Name]
		if visited {
			fmt.Println("visited, quindi skippo") //DEBUG
			continue
		}

		fmt.Println("NOT visited") //DEBUG
		nodeScore := getNodeScore(node)
		fmt.Println("NodeScore: ", nodeScore) //DEBUG
		if bestNode == nil || nodeScore > bestScore {
			bestNode = &nodes[i]
			bestScore = nodeScore
			fmt.Println("BestNode Corrente: ", bestNode.Name)
		}
	}

	if bestNode == nil {
		// Se tutti i nodi sono stati visitati
		delete(s.visitedNodesPerApp, appName)
		return s.getBestNode(pod, nodes)

	} else {
		s.visitedNodesPerApp[appName][bestNode.Name] = true
	}

	fmt.Println("BestNode Totale: ", bestNode.Name)
	return bestNode, nil
}

func getNodeScore(node v1.Node) float64 {
	cpuAvailable, _ := node.Status.Allocatable.Cpu().AsInt64()
	cpuScore := float64(cpuAvailable)

	memAvailable := node.Status.Allocatable.Memory().Value()
	memScore := float64(memAvailable) / float64(1024*1024) // Memoria in MiB

	// Pesare il punteggio della CPU e della memoria come desiderato
	// Ad esempio, qui usiamo un rapporto di 1:1 tra CPU e memoria
	totalScore := cpuScore + memScore

	return totalScore
}

func (s *CustomScheduler) assignPodToNode(pod *v1.Pod, node *v1.Node) error {
	binding := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		Target: v1.ObjectReference{
			APIVersion: "v1",
			Kind:       "Node",
			Name:       node.Name,
			UID:        types.UID(node.UID),
		},
	}

	err := s.clientset.CoreV1().Pods(pod.Namespace).Bind(context.TODO(), binding, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	fmt.Printf("Assigned pod %s/%s to node %s\n\n", pod.Namespace, pod.Name, node.Name)
	return nil
}

func (s *CustomScheduler) getLatencyThreshold(pod *v1.Pod) (int64, int64, error) {
	hardLatencyThreshold := int64(-1)
	softLatencyThreshold := int64(-1)
	var err error

	hardLatencyThresholdStr, ok := pod.Annotations["hard_max_latency"]
	if ok {
		hardLatencyThreshold, err = strconv.ParseInt(hardLatencyThresholdStr, 10, 64)
		if err != nil {
			return -1, -1, fmt.Errorf("error parsing hard max latency: %v", err)
		}
	}

	softLatencyThresholdStr, ok := pod.Annotations["soft_max_latency"]
	if ok {
		softLatencyThreshold, err = strconv.ParseInt(softLatencyThresholdStr, 10, 64)
		if err != nil {
			return hardLatencyThreshold, -1, fmt.Errorf("error parsing soft max latency: %v", err)
		}
	}

	return hardLatencyThreshold, softLatencyThreshold, nil
}

/*
func (s *CustomScheduler) getLatencyThreshold(pod *v1.Pod) (int64, int64, error) {
	affinity := pod.Spec.Affinity
	if affinity == nil || affinity.NodeAffinity == nil {
		return -1, -1, fmt.Errorf("unable to determine affinity from pod annotations")
	}

	hardLatencyThreshold := int64(-1)
	softLatencyThreshold := int64(-1)

	required := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if required != nil {
		for _, selectorTerm := range required.NodeSelectorTerms {
			for _, expression := range selectorTerm.MatchExpressions {
				if expression.Key == "max_latency" {
					if len(expression.Values) > 0 {
						latency, err := strconv.ParseInt(expression.Values[0], 10, 64)
						if err != nil {
							return -1, -1, fmt.Errorf("error parsing hard latency: %v", err)
						}
						hardLatencyThreshold = latency
					}
				}
			}
		}
	}

	preferred := affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution
	if preferred != nil {
		for _, weightedTerm := range preferred {
			for _, expression := range weightedTerm.Preference.MatchExpressions {
				if expression.Key == "max_latency" {
					if len(expression.Values) > 0 {
						latency, err := strconv.ParseInt(expression.Values[0], 10, 64)
						if err != nil {
							return -1, -1, fmt.Errorf("error parsing soft latency: %v", err)
						}
						softLatencyThreshold = latency
					}
				}
			}
		}
	}

	return hardLatencyThreshold, softLatencyThreshold, nil
}

func (s *CustomScheduler) getLatencyThreshold(pod *v1.Pod) (int64, int64, error) {
	for _, container := range pod.Spec.Containers {
		for _, envVar := range container.Env {
			if envVar.Name == "DEFAULT_LATENCY" {
				latency, err := strconv.ParseInt(envVar.Value, 10, 64)
				if err != nil {
					return 0, fmt.Errorf("error parsing latency: %v", err)
				}
				return latency, nil
			}
		}
	}
	return 0, fmt.Errorf("DEFAULT_LATENCY not found")
}
*/
