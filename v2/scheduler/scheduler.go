package main

import (
	"context"
	"fmt"
	"math/rand"
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
	clientset          *kubernetes.Clientset
	queue              workqueue.RateLimitingInterface
	informer           cache.SharedIndexInformer
	visitedNodesPerApp map[string]map[string]bool
	pauseDescheduler   chan PauseSignal
	pastMeasurements   map[string]map[string]int64
}

func NewCustomScheduler(clientset *kubernetes.Clientset, pauseDescheduler chan PauseSignal, pastMeasurements map[string]map[string]int64) *CustomScheduler {
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
		clientset:          clientset,
		informer:           informer,
		queue:              queue,
		visitedNodesPerApp: make(map[string]map[string]bool),
		pauseDescheduler:   pauseDescheduler,
		pastMeasurements:   pastMeasurements,
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
			/*
				if stopDescheduler {
					s.pauseDescheduler <- true
				} else {
					s.pauseDescheduler <- false
				}*/
		}()
	}
}

func (s *CustomScheduler) schedulePod(key string) error {
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
	var bestNodes []*v1.Node
	var bestScore float64

	appName, ok := pod.Labels["app"]
	if !ok {
		return nil, fmt.Errorf("unable to determine app name from pod labels")
	}
	//DEBUG
	fmt.Println("\nAppName: ", appName)
	fmt.Println("VisitedNodes for the App: ", s.visitedNodesPerApp[appName])

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
		if bestScore < nodeScore {
			bestNodes = []*v1.Node{&nodes[i]} // Reset the best nodes list
			bestScore = nodeScore
		} else if nodeScore == bestScore {
			bestNodes = append(bestNodes, &nodes[i]) // Add to the best nodes list
		}
		//DEBUG
		fmt.Print("BEST CURRENT NODES: [")
		for i := 0; i < len(bestNodes); i++ {
			fmt.Print(bestNodes[i].Name, ", ")
		}
		fmt.Println("].")
	}

	var bestNode *v1.Node
	if len(bestNodes) > 1 {
		randIndex := rand.Intn(len(bestNodes))
		bestNode = bestNodes[randIndex]
	} else if len(bestNodes) == 1 {
		bestNode = bestNodes[0]
	}

	if bestNode == nil {
		// Se tutti i nodi sono stati visitati
		bestLastCycleNode, err := s.getLastCycleBestNode(appName)
		if err != nil {
			fmt.Println("Impossible to getting the Best Last Cycle Node: ", err)
			fmt.Println("Proceding with classic schedule behaviour")
			//classic schedule behaviour:
			selectedNode, err := s.classicScheduleBehavior(pod, nodes)
			return selectedNode, err
			//delete(s.visitedNodesPerApp, appName)
			//return s.getBestNode(pod, nodes)
		}
		fmt.Println("Best Last Cycle Node: ", bestLastCycleNode.Name) //DEBUG
		delete(s.pastMeasurements, appName)                           //elimino le misure, mi servono solo per l'ultimo ciclo!
		// Verifica se il descheduler deve essere bloccato
		err = s.shouldPauseDescheduler(appName) //TODO: Da sistemare
		if err != nil {
			return nil, fmt.Errorf("Error in pause scheduler: %v", err)
		}
		fmt.Println() //DEBUG
		bestNode = bestLastCycleNode

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

func (s *CustomScheduler) shouldPauseDescheduler(appName string) error {
	nodes, err := s.clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if len(s.visitedNodesPerApp[appName]) == len(nodes.Items)-1 { //-1 perchè non devo considerare il control-node
		fmt.Println("SCHEDULER: TUTTI NODI VISITATI, BLOCCO DESCHEDULER PER L'APP: ", appName) //DEBUG
		s.pauseDescheduler <- PauseSignal{isPaused: true, appName: appName}
	}
	return err
}

func (s *CustomScheduler) getLastCycleBestNode(appName string) (*v1.Node, error) {
	var minNodeName string
	var minLat int64 = -1

	if len(s.pastMeasurements[appName]) == 0 {
		return nil, fmt.Errorf("No pastMeasurements => last cycle already completed")
	}
	fmt.Println("LastCycleLatencies: ", s.pastMeasurements[appName]) //DEBUG
	for nodeName, latency := range s.pastMeasurements[appName] {
		if minLat == -1 || latency < minLat {
			minNodeName = nodeName
			minLat = latency
		}
	}
	node, err := s.clientset.CoreV1().Nodes().Get(context.Background(), minNodeName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return node, nil

}

func (s *CustomScheduler) classicScheduleBehavior(pod *v1.Pod, nodes []v1.Node) (*v1.Node, error) {
	// Calcola il numero totale di nodi disponibili
	totalNodes := len(nodes) - 1
	var consideredNodes []v1.Node
	if totalNodes == 0 {
		return nil, fmt.Errorf("no nodes available")
	}

	for _, node := range nodes {
		if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok { //"Control Plane Ignorato"
			continue
		}
		consideredNodes = append(consideredNodes, node)
	}

	// Calcola un indice casuale per selezionare il nodo
	randomIndex := rand.Intn(totalNodes)

	// Seleziona il nodo casuale
	selectedNode := &consideredNodes[randomIndex]

	return selectedNode, nil
}
