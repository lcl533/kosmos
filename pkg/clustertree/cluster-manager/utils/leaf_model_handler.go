package utils

import (
	"context"
	"fmt"
	"net/http"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	kosmosv1alpha1 "github.com/kosmos.io/kosmos/pkg/apis/kosmos/v1alpha1"
	"github.com/kosmos.io/kosmos/pkg/utils"
)

// LeafModelHandler is the interface to handle the leafModel logic
type LeafModelHandler interface {
	// GetLeafMode returns the leafMode for a Cluster
	GetLeafMode() LeafMode

	// GetLeafNodes returns nodes in leaf cluster by the rootNode
	GetLeafNodes(ctx context.Context, rootNode *corev1.Node, selector kosmosv1alpha1.NodeSelector) (*corev1.NodeList, error)

	// GetLeafPods returns pods in leaf cluster by the rootNode
	GetLeafPods(ctx context.Context, rootNode *corev1.Node, selector kosmosv1alpha1.NodeSelector) (*corev1.PodList, error)

	// UpdateRootNodeStatus updates the node's status in root cluster
	UpdateRootNodeStatus(ctx context.Context, node []*corev1.Node, leafNodeSelector map[string]kosmosv1alpha1.NodeSelector) error

	// CreateRootNode creates the node in root cluster
	CreateRootNode(ctx context.Context, listenPort int32, gitVersion string) ([]*corev1.Node, map[string]kosmosv1alpha1.NodeSelector, error)
}

// ClassificationHandler handles the Classification leaf model
type ClassificationHandler struct {
	leafMode LeafMode
	Cluster  *kosmosv1alpha1.Cluster
	//LeafClient    client.Client
	//RootClient    client.Client
	RootClientset         kubernetes.Interface
	LeafClientset         kubernetes.Interface
	clusterConditionCache clusterConditionStore
}

// GetLeafMode returns the leafMode for a Cluster
func (h ClassificationHandler) GetLeafMode() LeafMode {
	return h.leafMode
}

// GetLeafNodes returns nodes in leaf cluster by the rootNode
func (h ClassificationHandler) GetLeafNodes(ctx context.Context, rootNode *corev1.Node, selector kosmosv1alpha1.NodeSelector) (nodesInLeaf *corev1.NodeList, err error) {
	listOption := metav1.ListOptions{}
	if h.leafMode == Party {
		listOption.LabelSelector = metav1.FormatLabelSelector(selector.LabelSelector)
	}

	if h.leafMode == Node {
		listOption.FieldSelector = fmt.Sprintf("metadata.name=%s", rootNode.Name)
	}

	nodesInLeaf, err = h.LeafClientset.CoreV1().Nodes().List(ctx, listOption)
	if err != nil {
		return nil, err
	}
	return nodesInLeaf, nil
}

// GetLeafPods returns pods in leaf cluster by the rootNode
func (h ClassificationHandler) GetLeafPods(ctx context.Context, rootNode *corev1.Node, selector kosmosv1alpha1.NodeSelector) (pods *corev1.PodList, err error) {
	if h.leafMode == Party {
		pods, err = h.LeafClientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
	} else if h.leafMode == Node {
		pods, err = h.LeafClientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{FieldSelector: fmt.Sprintf("spec.nodeName=%s", rootNode.Name)})
		if err != nil {
			return nil, err
		}
	} else {
		nodesInLeafs, err := h.GetLeafNodes(ctx, rootNode, selector)
		if err != nil {
			return nil, err
		}

		for _, node := range nodesInLeafs.Items {
			podsInNode, err := h.LeafClientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
				FieldSelector: fmt.Sprintf("spec.nodeName=%s", node.Name),
			})
			if err != nil {
				return nil, err
			}
			if pods == nil {
				pods = podsInNode
			} else {
				pods.Items = append(pods.Items, podsInNode.Items...)
			}
		}
	}
	return pods, nil
}

// UpdateRootNodeStatus updates the node's status in root cluster
func (h ClassificationHandler) UpdateRootNodeStatus(ctx context.Context, nodesInRoot []*corev1.Node, leafNodeSelector map[string]kosmosv1alpha1.NodeSelector) error {
	errors := []error{}
	for _, node := range nodesInRoot {
		nodeNameInRoot := node.Name
		listOptions := metav1.ListOptions{}
		if h.leafMode == Party {
			selector, ok := leafNodeSelector[nodeNameInRoot]
			if !ok {
				klog.Warningf("have no nodeSelector for the join node: v%", nodeNameInRoot)
				continue
			}
			listOptions.LabelSelector = metav1.FormatLabelSelector(selector.LabelSelector)
		} else if h.leafMode == Node {
			listOptions.FieldSelector = fmt.Sprintf("metadata.name=%s", nodeNameInRoot)
		}

		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			nodeInRoot, err := h.RootClientset.CoreV1().Nodes().Get(ctx, nodeNameInRoot, metav1.GetOptions{})
			if err != nil {
				// TODO: If a node is accidentally deleted, recreate it
				return fmt.Errorf("cannot get node in root cluster while update the join node status %s, err: %v", nodeNameInRoot, err)
			}

			nodesInLeaf, err := h.LeafClientset.CoreV1().Nodes().List(ctx, listOptions)
			if err != nil {
				// TODO: If a node is accidentally deleted, recreate it
				return fmt.Errorf("cannot get node in leaf cluster while update the join node %s status, err: %v", nodeNameInRoot, err)
			}
			if len(nodesInLeaf.Items) == 0 {
				// TODO: If a node is accidentally deleted, recreate it
				return fmt.Errorf("have no node in leaf cluster while update the join node %s status", nodeNameInRoot)
			}

			rootCopy := nodeInRoot.DeepCopy()
			var nodeInLeafTaints []corev1.Taint

			if h.leafMode == Node {
				rootCopy.Status = *nodesInLeaf.Items[0].Status.DeepCopy()
				nodeInLeafTaints = append(nodesInLeaf.Items[0].Spec.Taints, corev1.Taint{
					Key:    utils.KosmosNodeTaintKey,
					Value:  utils.KosmosNodeTaintValue,
					Effect: utils.KosmosNodeTaintEffect,
				})
			} else {
				online := h.getClusterHealthStatus()
				observedReadyConditons := getObservedReadyStatus(online)
				readyConditions := h.clusterConditionCache.thresholdAdjustedReadyCondition(h.Cluster, nodeInRoot, observedReadyConditons)
				readyCondition := FindStatusCondition(readyConditions)
				if !online && readyCondition.Status != corev1.ConditionTrue {
					klog.V(2).Infof("Cluster(%s) still offline after %s, ensuring offline is set.", h.Cluster.Name, h.clusterConditionCache.failureThreshold)
					rootCopy.Status.Conditions = readyConditions
					updateAddress, err := GetAddress(ctx, h.RootClientset, nodesInLeaf.Items[0].Status.Addresses)
					if err != nil {
						return err
					}
					rootCopy.Status.Addresses = updateAddress
					if _, err = h.RootClientset.CoreV1().Nodes().UpdateStatus(ctx, rootCopy, metav1.UpdateOptions{}); err != nil {
						return err
					}
					return nil
				}
				if online && readyCondition.Status == corev1.ConditionTrue {
					leafMasterReadyConditon := h.checkAllMasterNodesNotReady(ctx)
					rootCopy.Status.Conditions = leafMasterReadyConditon
				} else {
					rootCopy.Status.Conditions = readyConditions
				}
				// Aggregation the resources of the leaf nodes
				pods, err := h.GetLeafPods(ctx, rootCopy, leafNodeSelector[nodeNameInRoot])
				if err != nil {
					return fmt.Errorf("could not list pod in leaf cluster while update the join node %s status, err: %v", nodeNameInRoot, err)
				}
				clusterResources := utils.CalculateClusterResources(nodesInLeaf, pods)
				rootCopy.Status.Allocatable = clusterResources
				rootCopy.Status.Capacity = clusterResources
			}

			updateAddress, err := GetAddress(ctx, h.RootClientset, nodesInLeaf.Items[0].Status.Addresses)
			if err != nil {
				return err
			}

			rootCopy.Status.Addresses = updateAddress
			if _, err = h.RootClientset.CoreV1().Nodes().UpdateStatus(ctx, rootCopy, metav1.UpdateOptions{}); err != nil {
				return err
			}

			if h.leafMode == Node {
				err := updateTaints(h.RootClientset, nodeInLeafTaints, rootCopy.Name)
				if err != nil {
					return fmt.Errorf("update taints failed: %v", err)
				}
			}

			return nil
		})
		if err != nil {
			errors = append(errors, err)
		}
	}
	return utilerrors.NewAggregate(errors)
}

func updateTaints(client kubernetes.Interface, taints []corev1.Taint, nodeName string) error {
	node := corev1.Node{
		Spec: corev1.NodeSpec{
			Taints: taints,
		},
	}
	patchJSON, err := json.Marshal(node)
	if err != nil {
		return err
	}
	_, err = client.CoreV1().Nodes().Patch(context.TODO(), nodeName, types.MergePatchType, patchJSON, metav1.PatchOptions{})
	if err != nil {
		return err
	}
	return nil
}

func createNode(ctx context.Context, clientset kubernetes.Interface, clusterName, nodeName, gitVersion string, listenPort int32, leafModel LeafMode) (*corev1.Node, error) {
	nodeInRoot, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return nil, err
		}

		nodeInRoot = utils.BuildNodeTemplate(nodeName)
		nodeAnnotations := nodeInRoot.GetAnnotations()
		if nodeAnnotations == nil {
			nodeAnnotations = make(map[string]string, 1)
		}
		if leafModel == ALL {
			nodeAnnotations[nodeMode] = "one2cluster"
		}
		nodeAnnotations[utils.KosmosNodeOwnedByClusterAnnotations] = clusterName
		nodeInRoot.SetAnnotations(nodeAnnotations)

		nodeInRoot.Status.NodeInfo.KubeletVersion = gitVersion
		nodeInRoot.Status.DaemonEndpoints = corev1.NodeDaemonEndpoints{
			KubeletEndpoint: corev1.DaemonEndpoint{
				Port: listenPort,
			},
		}

		nodeInRoot, err = clientset.CoreV1().Nodes().Create(ctx, nodeInRoot, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
	}
	return nodeInRoot, nil
}

// CreateRootNode creates the node in root cluster
func (h ClassificationHandler) CreateRootNode(ctx context.Context, listenPort int32, gitVersion string) ([]*corev1.Node, map[string]kosmosv1alpha1.NodeSelector, error) {
	nodes := make([]*corev1.Node, 0)
	leafNodeSelectors := make(map[string]kosmosv1alpha1.NodeSelector)
	cluster := h.Cluster

	if h.leafMode == ALL {
		nodeNameInRoot := fmt.Sprintf("%s%s", utils.KosmosNodePrefix, cluster.Name)
		nodeInRoot, err := createNode(ctx, h.RootClientset, cluster.Name, nodeNameInRoot, gitVersion, listenPort, h.leafMode)
		if err != nil {
			return nil, nil, err
		}
		nodes = append(nodes, nodeInRoot)
		leafNodeSelectors[nodeNameInRoot] = kosmosv1alpha1.NodeSelector{}
	} else {
		for i, leafModel := range cluster.Spec.ClusterTreeOptions.LeafModels {
			var nodeNameInRoot string
			if h.leafMode == Node {
				nodeNameInRoot = leafModel.NodeSelector.NodeName
			} else {
				nodeNameInRoot = fmt.Sprintf("%v%v%v%v", utils.KosmosNodePrefix, leafModel.LeafNodeName, "-", i)
			}
			if len(nodeNameInRoot) > 63 {
				nodeNameInRoot = nodeNameInRoot[:63]
			}

			nodeInRoot, err := createNode(ctx, h.RootClientset, cluster.Name, nodeNameInRoot, gitVersion, listenPort, h.leafMode)
			if err != nil {
				return nil, nil, err
			}
			if h.leafMode == Party {
				nodeInRoot.Annotations[nodeMode] = "one2party"
			}

			nodes = append(nodes, nodeInRoot)
			leafNodeSelectors[nodeNameInRoot] = leafModel.NodeSelector
		}
	}

	return nodes, leafNodeSelectors, nil
}

// NewLeafModelHandler create a LeafModelHandler for Cluster
func NewLeafModelHandler(cluster *kosmosv1alpha1.Cluster, rootClientset, leafClientset kubernetes.Interface) LeafModelHandler {
	classificationModel := &ClassificationHandler{
		leafMode:      ALL,
		Cluster:       cluster,
		RootClientset: rootClientset,
		LeafClientset: leafClientset,
	}

	leafModels := cluster.Spec.ClusterTreeOptions.LeafModels

	if leafModels != nil && !reflect.DeepEqual(leafModels[0].NodeSelector, kosmosv1alpha1.NodeSelector{}) {
		if leafModels[0].NodeSelector.LabelSelector != nil && !reflect.DeepEqual(leafModels[0].NodeSelector.LabelSelector, metav1.LabelSelector{}) {
			// support nodeSelector mode
			classificationModel.leafMode = Party
		} else if leafModels[0].NodeSelector.NodeName != "" {
			classificationModel.leafMode = Node
		}
	}
	return classificationModel
}

// Perform a health check on the API server
func (h ClassificationHandler) getClusterHealthStatus() (online bool) {
	var healthStatus int
	resp := h.LeafClientset.Discovery().RESTClient().Get().AbsPath("/readyz").Do(context.TODO()).StatusCode(&healthStatus)
	if resp.Error() != nil {
		klog.Errorf("Health check failed.Current health status:%v, error is : %v ", healthStatus, resp.Error())
	}
	if healthStatus != http.StatusOK {
		klog.Warningf("Member cluster %v isn't healthy", h.Cluster.Name)
		return false
	}
	return true
}

// Returns the node status based on the health check results
func getObservedReadyStatus(online bool) []corev1.NodeCondition {
	if !online {
		return []corev1.NodeCondition{
			{
				Type:    corev1.NodeReady,
				Status:  corev1.ConditionFalse,
				Reason:  "ClusterNotReachable",
				Message: "cluster is not reachable.",
			},
		}
	}
	return []corev1.NodeCondition{
		{
			Type:    corev1.NodeReady,
			Status:  corev1.ConditionTrue,
			Reason:  "ClusterReady",
			Message: "cluster is online and ready to accept workloads.",
		},
	}
}
func (h ClassificationHandler) checkAllMasterNodesNotReady(ctx context.Context) []corev1.NodeCondition {
	//to do .....must check if LabelSelector: utils.LabelNodeRoleControlPlane is correct?
	nodes, err := h.LeafClientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: utils.LabelNodeRoleControlPlane})
	if err != nil {
		klog.Errorf("Error getting master nodes in leaf cluster: %v", err)
	}
	allMasterNotReady := true
	for _, node := range nodes.Items {
		masterNodeReady := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				masterNodeReady = true
				break
			}
		}
		if masterNodeReady {
			allMasterNotReady = false
		}
	}
	if allMasterNotReady {
		return []corev1.NodeCondition{
			{
				Type:    corev1.NodeReady,
				Status:  corev1.ConditionFalse,
				Reason:  "LeafNodesNotReady",
				Message: "All leaf cluster‘s master nodes are not ready.",
			},
		}
	}
	return utils.NodeConditions()
}
