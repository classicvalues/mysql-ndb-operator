// Copyright (c) 2020, Oracle and/or its affiliates.
//
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

package controllers

import (
	"encoding/json"
	"fmt"
	"time"

	apps "k8s.io/api/apps/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	"github.com/mysql/ndb-operator/pkg/apis/ndbcontroller/v1alpha1"
	"github.com/mysql/ndb-operator/pkg/constants"
	clientset "github.com/mysql/ndb-operator/pkg/generated/clientset/versioned"
	samplescheme "github.com/mysql/ndb-operator/pkg/generated/clientset/versioned/scheme"
	informers "github.com/mysql/ndb-operator/pkg/generated/informers/externalversions/ndbcontroller/v1alpha1"
	listers "github.com/mysql/ndb-operator/pkg/generated/listers/ndbcontroller/v1alpha1"
	"github.com/mysql/ndb-operator/pkg/ndb"
	"github.com/mysql/ndb-operator/pkg/resources"
)

const controllerAgentName = "ndb-controller"

const (
	// SuccessSynced is used as part of the Event 'reason' when a Foo is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a Foo fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource %q already exists and is not managed by Foo"
	// MessageResourceSynced is the message used for an Event fired when a Foo
	// is synced successfully
	MessageResourceSynced = "Ndb synced successfully"
)

// SyncContext stores all information collected in/for a single run of syncHandler
type SyncContext struct {
	resourceContext *resources.ResourceContext
	ndb             *v1alpha1.Ndb
	dataNodeSfSet   *apps.StatefulSet
}

// Controller is the main controller implementation for Ndb resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface

	// ndbclientset is a clientset for our own API group
	ndbclientset clientset.Interface

	statefulSetLister       appslisters.StatefulSetLister
	statefulSetListerSynced cache.InformerSynced

	ndbsLister listers.NdbLister
	ndbsSynced cache.InformerSynced

	mgmdController      StatefulSetControlInterface
	ndbdController      StatefulSetControlInterface
	configMapController ConfigMapControlInterface

	serviceLister       corelisters.ServiceLister
	serviceListerSynced cache.InformerSynced

	// podLister is able to list/get Pods from a shared
	// informer's store.
	podLister corelisters.PodLister
	// podListerSynced returns true if the Pod shared informer
	// has synced at least once.
	podListerSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}

// NewController returns a new Ndb controller
func NewController(
	kubeclientset kubernetes.Interface,
	ndbclientset clientset.Interface,
	statefulSetInformer appsinformers.StatefulSetInformer,
	serviceInformer coreinformers.ServiceInformer,
	podInformer coreinformers.PodInformer,
	configMapInformer coreinformers.ConfigMapInformer,
	ndbInformer informers.NdbInformer) *Controller {

	// Create event broadcaster
	// Add ndb-controller types to the default Kubernetes Scheme so Events can be
	// logged for ndb-controller types.
	utilruntime.Must(samplescheme.AddToScheme(scheme.Scheme))
	klog.V(4).Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:           kubeclientset,
		ndbclientset:            ndbclientset,
		ndbsLister:              ndbInformer.Lister(),
		ndbsSynced:              ndbInformer.Informer().HasSynced,
		statefulSetLister:       statefulSetInformer.Lister(),
		statefulSetListerSynced: statefulSetInformer.Informer().HasSynced,
		serviceLister:           serviceInformer.Lister(),
		serviceListerSynced:     serviceInformer.Informer().HasSynced,
		podLister:               podInformer.Lister(),
		podListerSynced:         podInformer.Informer().HasSynced,
		configMapController:     NewConfigMapControl(kubeclientset, configMapInformer),
		workqueue:               workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Ndbs"),
		recorder:                recorder,
	}

	klog.Info("Setting up event handlers")
	// Set up an event handler for when Ndb resources change
	ndbInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueNdb,
		UpdateFunc: func(old, new interface{}) {
			oldNdb := old.(*v1alpha1.Ndb)
			newNdb := new.(*v1alpha1.Ndb)

			if oldNdb.ResourceVersion == newNdb.ResourceVersion {
				// we don't do anything here - just log
				// this can happen e.g. if the timer kicks in - desireable to check state once in the while
			} else {
				klog.Infof("Resource version: %s -> %s", oldNdb.ResourceVersion, newNdb.ResourceVersion)
			}

			klog.Infof("Generation: %d -> %d", oldNdb.ObjectMeta.Generation, newNdb.ObjectMeta.Generation)
			if !equality.Semantic.DeepEqual(oldNdb.Spec, newNdb.Spec) {
				klog.Infof("Difference in spec: %d : %d", *oldNdb.Spec.NodeCount, *newNdb.Spec.NodeCount)
			} else if !equality.Semantic.DeepEqual(oldNdb.Status, newNdb.Status) {
				klog.Infof("Difference in status")
			} else {
				klog.Infof("Other difference in spec")
				//diff.ObjectGoPrintSideBySide(oldNdb, newNdb))
			}

			controller.enqueueNdb(newNdb)
		},
		DeleteFunc: func(obj interface{}) {
			var ndb *v1alpha1.Ndb
			switch obj.(type) {
			case *v1alpha1.Ndb:
				ndb = obj.(*v1alpha1.Ndb)
			case cache.DeletedFinalStateUnknown:
				del := obj.(cache.DeletedFinalStateUnknown).Obj
				ndb = del.(*v1alpha1.Ndb)
			}

			if ndb != nil {
				klog.Infof("Delete object received and queued %s/%s", ndb.Namespace, ndb.Name)
				controller.enqueueNdb(ndb)
			} else {
				klog.Infof("Unkown deleted object ignored")
			}
		},
	})

	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod := obj.(*v1.Pod)
			ls := labels.Set(pod.Labels)
			if !ls.Has(constants.ClusterLabel) {
				return
			}
			//s, _ := json.MarshalIndent(pod.Status, "", "  ")
			//klog.Infof("%s", string(s))
			klog.Infof("pod new %s: phase= %s, ip=%s", pod.Name, pod.Status.Phase, pod.Status.PodIP)
		},
		UpdateFunc: func(old, new interface{}) {
			newPod := new.(*v1.Pod)
			ls := labels.Set(newPod.Labels)
			if !ls.Has(constants.ClusterLabel) {
				return
			}

			//oldPod := old.(*v1.Pod)
			//s, _ := json.MarshalIndent(newPod.Status, "", "  ")
			klog.Infof("pod upd %s: phase= %s, ip=%s", newPod.Name, newPod.Status.Phase, newPod.Status.PodIP)
		},
		DeleteFunc: func(obj interface{}) {
		},
	})

	statefulSetInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleObject,
		UpdateFunc: func(old, new interface{}) {
			newStatefulSet := new.(*appsv1.StatefulSet)
			oldStatefulSet := old.(*appsv1.StatefulSet)
			if newStatefulSet.ResourceVersion == oldStatefulSet.ResourceVersion {
				return
			}
			controller.handleObject(new)
		},
		DeleteFunc: controller.handleObject,
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting Ndb controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh,
		c.ndbsSynced,
		c.statefulSetListerSynced,
		c.serviceListerSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	// Launch two workers to process Ndb resources
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		klog.Infof("Working on '%s'", key)
		// Run the syncHandler, passing it the namespace/name string of the
		// Foo resource to be synced.
		if err := c.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (c *Controller) updateClusterLabels(ndb *v1alpha1.Ndb, lbls labels.Set) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ndb.Labels = labels.Merge(labels.Set(ndb.Labels), lbls)
		_, updateErr :=
			c.ndbclientset.NdbcontrollerV1alpha1().Ndbs(ndb.Namespace).Update(ndb)
		if updateErr == nil {
			return nil
		}

		key := fmt.Sprintf("%s/%s", ndb.GetNamespace(), ndb.GetName())
		klog.V(4).Infof("Conflict updating Cluster labels. Getting updated Cluster %s from cache...", key)

		updated, err := c.ndbsLister.Ndbs(ndb.Namespace).Get(ndb.Name)
		if err != nil {
			klog.Errorf("Error getting updated Cluster %q: %v", key, err)
			return err
		}

		// Copy the Cluster so we don't mutate the cache.
		ndb = updated.DeepCopy()
		return updateErr
	})
}

/* TODO function should ensure useful and needed default values for
the ndb setup */
func (c *Controller) ensureDefaults(ndb *v1alpha1.Ndb) {

}

// ensureService ecreates a services if it doesn't exist
// returns
//    service eixsting or created
//    true if services was created
//    error if any such occured
func (c *Controller) ensureService(ndb *v1alpha1.Ndb, isMgmd bool, externalIP bool, name string) (*corev1.Service, bool, error) {

	svc, err := c.serviceLister.Services(ndb.Namespace).Get(name)

	if err == nil {
		return svc, true, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, false, err
	}

	klog.Infof("Creating a new Service for cluster %q",
		types.NamespacedName{Namespace: ndb.Namespace, Name: ndb.Name})
	svc = resources.NewService(ndb, isMgmd, externalIP, name)
	svc, err = c.kubeclientset.CoreV1().Services(ndb.Namespace).Create(svc)
	if err != nil {
		return nil, false, err
	}
	return svc, false, err
}

// ensure services creates services if they don't exist
// returns
//    array with services created
//    false if any services were created
//    error if any such occured
func (c *Controller) ensureServices(ndb *v1alpha1.Ndb) (*[](*corev1.Service), bool, error) {
	svcs := []*corev1.Service{}

	retExisted := true

	// create a headless service for management nodes
	svc, existed, err := c.ensureService(ndb, true, false, ndb.GetManagementServiceName())
	if err != nil {
		return nil, false, err
	}
	retExisted = retExisted && existed
	svcs = append(svcs, svc)

	// create a loadbalancer service for management servers
	svc, existed, err = c.ensureService(ndb, true, true, ndb.GetManagementServiceName()+"-ext")
	if err != nil {
		return nil, false, err
	}

	// create a headless service for data nodes
	svc, existed, err = c.ensureService(ndb, false, false, ndb.GetDataNodeServiceName())
	svcs = append(svcs, svc)
	if err != nil {
		return nil, false, err
	}
	retExisted = retExisted && existed
	svcs = append(svcs, svc)

	return &svcs, retExisted, nil
}

// ensureManagementServerStatefulSet creates the stateful set for management servers if it doesn't exist
// returns
//    new or existing statefulset
//    reports true if it existed
//    or returns an error if something went wrong
func (c *Controller) ensureManagementServerStatefulSet(rc *resources.ResourceContext, ndb *v1alpha1.Ndb) (*appsv1.StatefulSet, bool, error) {

	// create the management stateful set if it doesn't exist
	if c.mgmdController == nil {
		mgmdSfSet := resources.NewMgmdStatefulSet(ndb)
		c.mgmdController =
			&realStatefulSetControl{
				client:            c.kubeclientset,
				statefulSetLister: c.statefulSetLister,
				statefulSetType:   mgmdSfSet}
	}

	sfset, existed, err := c.mgmdController.EnsureStatefulSet(rc, ndb)
	if err != nil {
		return nil, existed, err
	}

	// If the StatefulSet is not controlled by this Ndb resource, we should log
	// a warning to the event recorder and return error msg.
	if !metav1.IsControlledBy(sfset, ndb) {
		msg := fmt.Sprintf(MessageResourceExists, sfset.Name)
		c.recorder.Event(ndb, corev1.EventTypeWarning, ErrResourceExists, msg)
		return nil, existed, fmt.Errorf(msg)
	}

	return sfset, existed, err
}

// ensureDataNodeStatefulSet creates the stateful set for data node if it doesn't exist
// returns
//    new or existing statefulset
//    reports true if it existed
//    or returns an error if something went wrong
func (c *Controller) ensureDataNodeStatefulSet(rc *resources.ResourceContext, ndb *v1alpha1.Ndb) (*appsv1.StatefulSet, bool, error) {

	//TODO: should probably create controller earlier
	if c.ndbdController == nil {
		ndbdSfSet := resources.NewNdbdStatefulSet(ndb)
		c.ndbdController =
			&realStatefulSetControl{
				client:            c.kubeclientset,
				statefulSetLister: c.statefulSetLister,
				statefulSetType:   ndbdSfSet}
	}

	sfset, existed, err := c.ndbdController.EnsureStatefulSet(rc, ndb)
	if err != nil {
		return nil, existed, err
	}

	// If the StatefulSet is not controlled by this Ndb resource, we should log
	// a warning to the event recorder and return error msg.
	if !metav1.IsControlledBy(sfset, ndb) {
		msg := fmt.Sprintf(MessageResourceExists, sfset.Name)
		c.recorder.Event(ndb, corev1.EventTypeWarning, ErrResourceExists, msg)
		return nil, existed, fmt.Errorf(msg)
	}

	return sfset, existed, err
}

// ensurePodDisruptionBudget creates a PDB if it doesn't exist
// returns
//    new or existing PDB
//    reports true if it existed
//    or returns an error if something went wrong
func (c *Controller) ensurePodDisruptionBudget(ndb *v1alpha1.Ndb) (*policyv1beta1.PodDisruptionBudget, bool, error) {

	pdbs := c.kubeclientset.PolicyV1beta1().PodDisruptionBudgets(ndb.Namespace)
	pdb, err := pdbs.Get(ndb.GetPodDisruptionBudgetName(), metav1.GetOptions{})
	if err == nil {
		return pdb, true, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, false, err
	}

	klog.Infof("Creating a new PodDisruptionBudget for Data Nodes of Cluster %q",
		types.NamespacedName{Namespace: ndb.Namespace, Name: ndb.Name})
	pdb = resources.NewPodDisruptionBudget(ndb)
	pdb, err = pdbs.Create(pdb)

	if err != nil {
		return nil, false, err
	}

	return pdb, false, err
}

func (c *Controller) ensureDataNodeConfigVersion(ndbobj *v1alpha1.Ndb, cs *ndb.ClusterStatus, wantedGeneration int) syncResult {

	// we go through all data nodes and see if they are on the latest config version
	// we do this "ndb replica" wise, i.e. we iterate first through first nodes in each node group, then second, etc.
	ct := ndb.CreateClusterTopologyByReplicaFromClusterStatus(cs)

	if ct == nil {
		err := fmt.Errorf("Internal error: could not extract topology from cluster status")
		return errorWhileProcssing(err)
	}

	reduncanyLevel := ct.GetNumberOfReplicas()
	api := &ndb.Mgmclient{}
	err := api.Connect()
	if err != nil {
		return errorWhileProcssing(err)
	}
	defer api.Disconnect()

	for replica := 0; replica < reduncanyLevel; replica++ {

		restartIDs := []int{}

		nodeIDs := ct.GetNodeIDsFromReplica(replica)
		for nodeID := range *nodeIDs {
			nodeConfigGeneration := api.GetConfigVersionFromNode(nodeID)

			if wantedGeneration != nodeConfigGeneration {
				// node is on wrong config generation
				restartIDs = append(restartIDs, nodeID)
			}
		}

		if len(restartIDs) > 0 {
			s, _ := json.Marshal(restartIDs)
			klog.Infof("Identified %d nodes with wrong version in replica %d: %s",
				len(restartIDs), replica, s)

			_, err := api.StopNodes(&restartIDs)
			if err != nil {
				klog.Infof("Error restarting replica %d nodes %s", replica, s)
				return errorWhileProcssing(err)
			}

			// nodes started to stop - exit sync loop
			return finishProcessing()
		}

		klog.Infof("All datanodes nodes in replica %d have desired config version %d", replica, wantedGeneration)
	}

	return continueProcessing()
}

func (c *Controller) ensureManagementServerConfigVersion(ndbobj *v1alpha1.Ndb, wantedGeneration int) syncResult {

	klog.Infof("Ensuring Management Server has correct config version")

	api := &ndb.Mgmclient{}

	// management server have the first nodeids
	// TODO: when we'll ever scale the number of management servers then this
	// needs to be changed to actually currently configured management servers
	// ndbobj has "desired" number of management servers
	for nodeID := 1; nodeID <= (int)(ndbobj.GetManagementNodeCount()); nodeID++ {

		// TODO : we use this function so far during test operator from outside cluster
		// we try connecting via load balancer until we connect to correct wanted node
		err := api.ConnectToNodeId(nodeID)
		if err != nil {
			klog.Errorf("No contact to management server to desired management server with node id %d established", nodeID)
			return errorWhileProcssing(err)
		}

		defer api.Disconnect()

		version := api.GetConfigVersion()
		if version == wantedGeneration {
			klog.Infof("Management server with node id %d has desired version %d",
				nodeID, version)

			// node has right version, continue to process next node
			continue
		}

		klog.Infof("Management server with node id %d has different version %d than desired %d",
			nodeID, version, wantedGeneration)

		// we are not in degraded in state
		// management server with nodeId was so nice to reveal all information
		// now we kill it - pod should terminate and restarted with updated config map and management server
		nodeIDs := []int{nodeID}
		_, err = api.StopNodes(&nodeIDs)

		// we do one at a time - exit here and wait for next reconcilation
		return finishProcessing()
	}

	// if we end up here then both mgm servers are on latest version, continue processing other sync steps
	return continueProcessing()
}

// checkPodStatus returns false if any container in pod is not ready
// TODO - should also look out for hanging or weird states
func (c *Controller) checkPodStatus(ndbobj *v1alpha1.Ndb) (bool, error) {

	klog.Infof("check Pod status")

	sel4ndb := labels.SelectorFromSet(ndbobj.GetLabels())
	pods, err := c.podLister.List(sel4ndb)
	if err != nil {
		return false, apierrors.NewNotFound(v1alpha1.Resource("Pod"), sel4ndb.String())
	}

	for _, pod := range pods {
		status := pod.Status
		statuses := status.ContainerStatuses
		for _, status := range statuses {
			if status.Name != "ndbd" && status.Name != "mgmd" {
				continue
			}
			if !status.Ready {
				return false, nil
			}
		}
	}

	return true, nil
}

func (c *Controller) ensureClusterLabel(ndb *v1alpha1.Ndb) (*labels.Set, bool, error) {
	// Ensure that the required labels are set on the cluster.
	sel4ndb := labels.SelectorFromSet(ndb.GetLabels())
	set := labels.Set(ndb.Labels)
	if sel4ndb.Matches(set) {
		return &set, true, nil
	}

	klog.Infof("Setting labels on cluster %s", sel4ndb.String())
	err := c.updateClusterLabels(ndb.DeepCopy(), ndb.GetLabels())
	if err != nil {
		return nil, false, err
	}

	return &set, false, nil

}

func (c *Controller) getClusterState() (*ndb.ClusterStatus, error) {

	api := &ndb.Mgmclient{}

	err := api.Connect()

	if err != nil {
		klog.Errorf("No contact to management server")
		return nil, err
	}

	defer api.Disconnect()

	// check if management nodes report a degraded cluster state
	cs, err := api.GetStatus()
	if err != nil {
		klog.Errorf("Error getting cluster status from mangement server: %s", err)
		return nil, err
	}

	return cs, nil
}

// checkClusterState checks the cluster state and whether its in a managable state
// e.g. - we don't want to touch it with rolling out new versions when not all data nodes are up
func (c *Controller) checkClusterState(sc *SyncContext, cs *ndb.ClusterStatus) syncResult {

	// cluster is okay if all active node groups have all data nodes up

	// during scaling ndb CRD will have more nodes configured
	// this will already be written to config file in a first sync step
	// but statefulsets will adopt no of replicas as last step before node group is created
	nodeGroupsUp := cs.NumberNodegroupsFullyUp(int(sc.resourceContext.ReduncancyLevel))
	numberOfDataNodes := nodeGroupsUp * int(sc.resourceContext.ReduncancyLevel)

	if int(*sc.dataNodeSfSet.Spec.Replicas) == numberOfDataNodes {
		// all nodes that should be up have all nodes running
		return continueProcessing()
	}
	return finishProcessing()
}

// allResourcesExisted returns falls if any resource map is false (resource was created)
func (c *Controller) allResourcesExisted(resourceMap *map[string]bool) bool {

	retExisted := true
	for res, existed := range *resourceMap {
		if existed {
			klog.Infof("Resource %s: existed", res)
		} else {
			klog.Infof("Resource %s: created", res)
			retExisted = false
		}
	}
	return retExisted
}

// ensureAllResources creates all resources if they do not exist
// the SyncContext struct will be filled with resource objects newly created or fetched
// it returns
//   false if any resource did not exist
//   an error if such occurs during processing
//
// Resource creation as all other steps in syncHandler need to be idempotent.
//
// However, creation of multiple resources is obviously not one large atomic operation.
// ndb.Status updates based on resources created can't be done atomically either.
// The creation processes could any time be disrupted by crashes, termination or (temp) errors.
//
// The goal is to create a consistent setup.
// The cluster configuration (file) in config map needs to match e.g. the no of replicas
// the in stateful sets even though ndb.Spec could change between config map and sfset creation.
//
// In order to solve these issues the configuration store in the config file is considered
// the source of the truth during the entire creation process. Only after all resources once
// successfully created changes to the ndb.Spec will be considered by the syncHandler.
func (c *Controller) ensureAllResources(syncContext *SyncContext) (*resources.ResourceContext, bool, error) {

	allExisted := make(map[string]bool)

	// create labels on ndb resource
	// TODO - not sure if we need a cluster level label on the CRD
	//      causes an update event looping us in here again
	var err error
	if _, allExisted["labels"], err = c.ensureClusterLabel(syncContext.ndb); err != nil {
		return nil, false, err
	}

	// create services for management server and data node statefulsets
	// with respect to idempotency and atomicy service creation is always safe as it
	// only uses the immutable CRD name
	// service needs to be created and present when creating stateful sets
	if _, allExisted["services"], err = c.ensureServices(syncContext.ndb); err != nil {
		return nil, false, err
	}

	// create pod disruption budgets
	if _, allExisted["poddisruptionservice"], err = c.ensurePodDisruptionBudget(syncContext.ndb); err != nil {
		return nil, false, err
	}

	// create config map if not exist
	var cm *corev1.ConfigMap
	if cm, allExisted["configmap"], err = c.configMapController.EnsureConfigMap(syncContext.ndb); err != nil {
		return nil, false, err
	}

	// get config string from config
	// this and following step could be avoided since we in most cases just created the config map
	// however, resource creation (happens once) or later modification as such is probably the unlikely case
	// much more likely in all cases is that the config map already existed
	configString, err := c.configMapController.ExtractConfig(cm)
	if err != nil {
		// TODO - this would be a very serious internal error
		return nil, false, err
	}

	resourceContext, err := resources.NewResourceContextFromConfiguration(configString)

	// create the management stateful set if it doesn't exist
	if _, allExisted["datanodestatefulset"], err = c.ensureManagementServerStatefulSet(resourceContext, syncContext.ndb); err != nil {
		return nil, false, err
	}

	// create the data node stateful set if it doesn't exist
	if syncContext.dataNodeSfSet, allExisted["mgmstatefulset"], err = c.ensureDataNodeStatefulSet(resourceContext, syncContext.ndb); err != nil {
		return nil, false, err
	}

	return resourceContext, c.allResourcesExisted(&allExisted), nil
}

// syncHandler is the main reconcilliation function
//   driving cluster towards desired configuration
//
// - synchronization happens in multiple steps
// - not all actions are taking in one call of syncHandler
//
// main principle:
// the desired state in Ndb CRD must be reflected in the Ndb configuration file
// and the cluster state will be adopted *to that config file first*
// before new changes from Ndb CRD are accepted
//
// Sync steps
//
// 1. ensure all resources are correctly created
// 2. ensure cluster is fully up and running and not in a degraded state
//    before rolling out any changes
// 3. drive cluster components towards the configuration previously
//    written to the configuration file
// 4. only after complete cluster is aligned with configuration file
//    new changes from Ndb CRD are written to a new version of the config file
// 5. update status of the CRD
func (c *Controller) syncHandler(key string) error {

	klog.Infof("Sync handler: %s", key)

	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the Ndb resource with this namespace/name
	ndbOrg, err := c.ndbsLister.Ndbs(namespace).Get(name)
	if err != nil {
		klog.Infof("Ndb does not exist as resource, %s", name)
		// The Ndb resource may no longer exist, in which case we stop
		// processing.
		if apierrors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("ndb '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}

	// take a copy and process that for the update at the end
	// make all changes to the copy only and patch the original in the end
	ndb := ndbOrg.DeepCopy()

	syncContext := SyncContext{ndb: ndb}

	// create all resources necessary to start and run cluster
	resourceContext, existed, err := c.ensureAllResources(&syncContext)
	if err != nil {
		return err
	}

	// we do not take further action and exit here if any resources still had to be created
	// pods will need to time to start, etc.
	//
	// since no error occured all resources should have been successfully created
	// TODO: we should take note of it by updating the ndb.Status with a creationTimestamp
	// - then we can check that later and avoid checking if all resources existed
	// - could be also good to always check if individual resources were deleted
	//   but re-creating them could be tricky due to resource dependencies bringing us to chaotic state
	// - if individual resources are missing after creation that would be a major error for now
	if !existed {
		return nil
	}

	syncContext.resourceContext = resourceContext

	// at this point all resources were created already
	// some pods might still not be fully up and running
	// cluster potentially not (fully) started yet

	if ready, _ := c.checkPodStatus(ndb); !ready {
		// if not all pods are ready yet there is no sense in processing config changes
		klog.Infof("Cluster has not all pods ready - exit sync and return later")
		return nil
	}

	//
	// as of here actions will only be taken if cluster is in a good state
	//

	clusterState, err := c.getClusterState()
	if err != nil {
		return err
	}

	if sr := c.checkClusterState(&syncContext, clusterState); sr.finished() {
		klog.Infof("Cluster is not reported to be fully running - exit sync and return here later")
		// TODO - introduce a re-schedule event
		return sr.getError() // return error if any
	}

	// sync handler does not accept new configurations from Ndb CRD
	// before previous configuration changes are not completed
	// start by aligning cluster to the configuration *in the config map* previously applied
	// only if everything is in line with that configuration
	// a new configuration from the Ndb CRD is accepted and written to the config map

	// make sure management server(s) have the correct config version
	if sr := c.ensureManagementServerConfigVersion(ndb, int(resourceContext.ConfigGeneration)); sr.finished() {
		return sr.getError()
	}

	// make sure all data nodes have the correct config version
	// data nodes a restarted with respect to
	if sr := c.ensureDataNodeConfigVersion(ndb, clusterState, int(resourceContext.ConfigGeneration)); sr.finished() {
		return sr.getError()
	}

	// If this number of the members on the Cluster does not equal the
	// current desired replicas on the StatefulSet, we should update the
	// StatefulSet resource.
	if syncContext.resourceContext.GetDataNodeCount() != uint32(*syncContext.dataNodeSfSet.Spec.Replicas) {
		nsName := types.NamespacedName{Namespace: namespace, Name: name}
		klog.Infof("Updating %q: DataNodes=%d statefulSetReplicas=%d",
			nsName, *ndb.Spec.NodeCount, *syncContext.dataNodeSfSet.Spec.Replicas)
		if syncContext.dataNodeSfSet, err = c.ndbdController.Patch(syncContext.resourceContext, ndb, syncContext.dataNodeSfSet); err != nil {
			// Requeue the item so we can attempt processing again later.
			// This could have been caused by a temporary network failure etc.
			return err
		}
	}

	// at this stage all resources are ensured to be
	// aligned with the configuration *in the config map*
	c.recorder.Event(ndb, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)

	// check if configuration in config map is still the desired from the Ndb CRD
	// if not then apply a new version

	klog.Infof("Config in config map config is \"%s\", new: \"%d\"",
		resourceContext.ConfigHash, resourceContext.ConfigGeneration)

	// calculated the hash of the new config to see if ndb.Spec changed against whats in the config map
	newConfigHash, err := ndb.CalculateNewConfigHash()
	if err != nil {
		klog.Errorf("Error calculating hash.")
		return err
	}

	if resourceContext.ConfigHash != newConfigHash {
		klog.Infof("Config received is different from config map config. config map: \"%s\", new: \"%s\"",
			resourceContext.ConfigHash, newConfigHash)

		_, err := c.configMapController.PatchConfigMap(ndb)
		if err != nil {
			klog.Infof("Failed to patch config map")
			return err
		}
	}

	// Finally, we update the status block of the Ndb resource to reflect the
	// current state of the world
	err = c.updateNdbStatus(ndb)
	if err != nil {
		klog.Errorf("updating status failed: %v", err)
		return err
	}

	klog.Infof("Returning from syncHandler")

	return nil
}

func updatePodForTest(pod *v1.Pod) *v1.Pod {
	t := time.Now()

	ann := map[string]string{
		"test": t.Format(time.UnixDate),
	}

	pod.Annotations = ann
	/*
		for idx, container := range pod.Spec.Containers {
			if container.Name == targetContainer {
				pod.Spec.Containers[idx].Image = newAgentImage
				break
			}
		}
	*/
	return pod
}

// PatchPod perform a direct patch update for the specified Pod.
func patchPod(kubeClient kubernetes.Interface, oldData *corev1.Pod, newData *corev1.Pod) (*corev1.Pod, error) {
	currentPodJSON, err := json.Marshal(oldData)
	if err != nil {
		return nil, err
	}

	updatedPodJSON, err := json.Marshal(newData)
	if err != nil {
		return nil, err
	}

	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(currentPodJSON, updatedPodJSON, corev1.Pod{})
	if err != nil {
		return nil, err
	}
	klog.V(4).Infof("Patching Pod %q: %s", types.NamespacedName{Namespace: oldData.Namespace, Name: oldData.Name}, string(patchBytes))

	result, err := kubeClient.CoreV1().Pods(oldData.Namespace).Patch(oldData.Name, types.StrategicMergePatchType, patchBytes)
	if err != nil {
		return nil, apierrors.NewNotFound(v1alpha1.Resource("Pod"), "failed patching pod")
	}

	return result, nil
}

func (c *Controller) podListing(ndb *v1alpha1.Ndb) error {

	sel4ndb := labels.SelectorFromSet(ndb.GetLabels())
	pods, err := c.podLister.List(sel4ndb)
	if err != nil {
		return apierrors.NewNotFound(v1alpha1.Resource("Pod"), sel4ndb.String())
	}
	for _, pod := range pods {
		//klog.Infof("Ndb pod '%s/%s'", pod.Namespace, pod.Name)
		newPod := updatePodForTest(pod.DeepCopy())
		pod, err = patchPod(c.kubeclientset, pod, newPod)
		if err != nil {
			return apierrors.NewNotFound(v1alpha1.Resource("Pod"), "upgrade operator version: PatchPod failed")
		}

	}

	return nil
}

func (c *Controller) updateNdbStatus(ndb *v1alpha1.Ndb) error {

	// NEVER modify objects from the store. It's a read-only, local cache.
	// You can use DeepCopy() to make a deep copy of original object and modify this copy
	// Or create a copy manually for better performance

	// we already received a copy here

	updateErr := wait.ExponentialBackoff(retry.DefaultBackoff, func() (ok bool, err error) {

		klog.Infof("Updating ndb cluster status")

		ndb.Status.LastUpdate = metav1.NewTime(time.Now())
		ndb.Status.ProcessedGeneration = ndb.ObjectMeta.Generation

		// If the CustomResourceSubresources feature gate is not enabled,
		// we must use Update instead of UpdateStatus to update the Status block of the Ndb resource.
		// UpdateStatus will not allow changes to the Spec of the resource,
		// which is ideal for ensuring nothing other than resource status has been updated.
		//_, err = c.ndbclientset.NdbcontrollerV1alpha1().Ndbs(ndb.Namespace).Update(ndb)

		_, err = c.ndbclientset.NdbcontrollerV1alpha1().Ndbs(ndb.Namespace).UpdateStatus(ndb)
		if err == nil {
			return true, nil
		}
		if !errors.IsConflict(err) {
			return false, err
		}

		updated, err := c.ndbclientset.NdbcontrollerV1alpha1().Ndbs(ndb.Namespace).Get(ndb.Name, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("failed to get Ndb %s/%s: %v", ndb.Namespace, ndb.Name, err)
			return false, err
		}
		ndb = updated.DeepCopy()
		return false, nil
	})

	if updateErr != nil {
		klog.Errorf("failed to update Ndb %s/%s: %v", ndb.Namespace, ndb.Name, updateErr)
		return updateErr
	}
	return nil
}

func (c *Controller) onDeleteNdb(ndb *v1alpha1.Ndb) {}

/*
	enqueueNdb takes a Ndb resource and converts it into a namespace/name
   	string which is then put onto the work queue. This method should *not* be
   	passed resources of any type other than Ndb.
*/
func (c *Controller) enqueueNdb(obj interface{}) {
	var key string
	var err error

	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.Infof("Processing Ndb: %s", key)
	c.workqueue.Add(key)
}

/*
   handleObject will take any resource implementing metav1.Object and attempt
   to find the Ndb resource that 'owns' it. It does this by looking at the
   objects metadata.ownerReferences field for an appropriate OwnerReference.
   It then enqueues that Foo resource to be processed. If the object does not
   have an appropriate OwnerReference, it will simply be skipped.
*/
func (c *Controller) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		klog.V(4).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}
	klog.Infof("Processing object: %s", object.GetName())
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		// If this object is not owned by a Ndb, we should not do anything more
		// with it.
		if ownerRef.Kind != "Ndb" {
			return
		}

		ndb, err := c.ndbsLister.Ndbs(object.GetNamespace()).Get(ownerRef.Name)
		if err != nil {
			klog.Infof("ignoring orphaned object '%s' of ndb '%s'", object.GetSelfLink(), ownerRef.Name)
			return
		}

		klog.Infof("Ignoring object: %s", ndb.GetName())
		//c.enqueueNdb(ndb)
		return
	}
}
