package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coredns/coredns/plugin/kubernetes/object"

	api "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crdclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	gtwapi "sigs.k8s.io/gateway-api/apis/v1"
	gtwapiclientset "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

const (
	podIPIndex                = "PodIP"
	svcNameNamespaceIndex     = "ServiceNameNamespace"
	svcIPIndex                = "ServiceIP"
	svcExtIPIndex             = "ServiceExternalIP"
	gatewayNameNamespaceIndex = "GatewayNameNamespace"
	gatewayAddressIndex       = "GatewayAddress"
	epNameNamespaceIndex      = "EndpointNameNamespace"
	epIPIndex                 = "EndpointsIP"
	kubeServiceCRDName        = "gateways.gateway.networking.k8s.io"
)

type dnsController interface {
	ServiceList() []*object.Service
	EndpointsList() []*object.Endpoints
	GatewayIndex(string) []*object.Gateway
	GatewayAddressIndex(ip string) []*object.Gateway
	SvcIndex(string) []*object.Service
	SvcIndexReverse(string) []*object.Service
	SvcExtIndexReverse(string) []*object.Service
	PodIndex(string) []*object.Pod
	EpIndex(string) []*object.Endpoints
	EpIndexReverse(string) []*object.Endpoints

	GetNodeByName(context.Context, string) (*api.Node, error)
	GetNamespaceByName(string) (*object.Namespace, error)

	Run()
	HasSynced() bool
	Stop() error

	// Modified returns the timestamp of the most recent changes to services.  If the passed bool is true, it should
	// return the timestamp of the most recent changes to services with external facing IP addresses
	Modified(bool) int64
}

type dnsControl struct {
	// modified tracks timestamp of the most recent changes
	// It needs to be first because it is guaranteed to be 8-byte
	// aligned ( we use sync.LoadAtomic with this )
	modified int64
	// extModified tracks timestamp of the most recent changes to
	// services with external facing IP addresses
	extModified int64

	client    kubernetes.Interface
	crdClient crdclientset.Interface
	gtwClient gtwapiclientset.Interface

	selector          labels.Selector
	namespaceSelector labels.Selector

	svcController            cache.Controller
	podController            cache.Controller
	epController             cache.Controller
	nsController             cache.Controller
	crdController            cache.Controller
	gatewayController        cache.Controller
	gatewayControllerStarted atomic.Bool

	svcLister     cache.Indexer
	gatewayLister cache.Indexer
	crdLister     cache.Indexer
	podLister     cache.Indexer
	epLister      cache.Indexer
	nsLister      cache.Store

	// stopLock is used to enforce only a single call to Stop is active.
	// Needed because we allow stopping through an http endpoint and
	// allowing concurrent stoppers leads to stack traces.
	stopLock sync.Mutex
	shutdown bool
	stopCh   chan struct{}

	zones            []string
	endpointNameMode bool
	readGatewayAPI   bool
}

type dnsControlOpts struct {
	initPodCache       bool
	initEndpointsCache bool
	ignoreEmptyService bool
	readGatewayAPI     bool
	mirrorGatewayToSvc bool

	// Label handling.
	labelSelector          *meta.LabelSelector
	selector               labels.Selector
	namespaceLabelSelector *meta.LabelSelector
	namespaceSelector      labels.Selector

	zones            []string
	endpointNameMode bool
}

// newdnsController creates a controller for CoreDNS.
func newdnsController(ctx context.Context, kubeClient kubernetes.Interface, crdClient crdclientset.Interface, gtwClient gtwapiclientset.Interface, opts dnsControlOpts) *dnsControl {
	dns := dnsControl{
		client:            kubeClient,
		crdClient:         crdClient,
		gtwClient:         gtwClient,
		selector:          opts.selector,
		namespaceSelector: opts.namespaceSelector,
		stopCh:            make(chan struct{}),
		zones:             opts.zones,
		endpointNameMode:  opts.endpointNameMode,
		readGatewayAPI:    opts.readGatewayAPI,
	}

	if dns.readGatewayAPI {
		setupGatewayController(ctx, kubeClient, &dns)
	}

	dns.svcLister, dns.svcController = object.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc:  serviceListFunc(ctx, dns.client, api.NamespaceAll, dns.selector),
			WatchFunc: serviceWatchFunc(ctx, dns.client, api.NamespaceAll, dns.selector),
		},
		&api.Service{},
		cache.ResourceEventHandlerFuncs{AddFunc: dns.Add, UpdateFunc: dns.Update, DeleteFunc: dns.Delete},
		cache.Indexers{svcNameNamespaceIndex: svcNameNamespaceIndexFunc, svcIPIndex: svcIPIndexFunc, svcExtIPIndex: svcExtIPIndexFunc},
		object.DefaultProcessor(object.ToService, nil),
	)

	podLister, podController := object.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc:  podListFunc(ctx, dns.client, api.NamespaceAll, dns.selector),
			WatchFunc: podWatchFunc(ctx, dns.client, api.NamespaceAll, dns.selector),
		},
		&api.Pod{},
		cache.ResourceEventHandlerFuncs{AddFunc: dns.Add, UpdateFunc: dns.Update, DeleteFunc: dns.Delete},
		cache.Indexers{podIPIndex: podIPIndexFunc},
		object.DefaultProcessor(object.ToPod, nil),
	)
	dns.podLister = podLister
	if opts.initPodCache {
		dns.podController = podController
	}

	epLister, epController := object.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc:  endpointSliceListFunc(ctx, dns.client, api.NamespaceAll, dns.selector),
			WatchFunc: endpointSliceWatchFunc(ctx, dns.client, api.NamespaceAll, dns.selector),
		},
		&discovery.EndpointSlice{},
		cache.ResourceEventHandlerFuncs{AddFunc: dns.Add, UpdateFunc: dns.Update, DeleteFunc: dns.Delete},
		cache.Indexers{epNameNamespaceIndex: epNameNamespaceIndexFunc, epIPIndex: epIPIndexFunc},
		object.DefaultProcessor(object.EndpointSliceToEndpoints, dns.EndpointSliceLatencyRecorder()),
	)
	dns.epLister = epLister
	if opts.initEndpointsCache {
		dns.epController = epController
	}

	dns.nsLister, dns.nsController = object.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc:  namespaceListFunc(ctx, dns.client, dns.namespaceSelector),
			WatchFunc: namespaceWatchFunc(ctx, dns.client, dns.namespaceSelector),
		},
		&api.Namespace{},
		cache.ResourceEventHandlerFuncs{},
		cache.Indexers{},
		object.DefaultProcessor(object.ToNamespace, nil),
	)

	return &dns
}

func (dns *dnsControl) EndpointsLatencyRecorder() *object.EndpointLatencyRecorder {
	return &object.EndpointLatencyRecorder{
		ServiceFunc: func(o meta.Object) []*object.Service {
			return dns.SvcIndex(object.ServiceKey(o.GetName(), o.GetNamespace()))
		},
	}
}
func (dns *dnsControl) EndpointSliceLatencyRecorder() *object.EndpointLatencyRecorder {
	return &object.EndpointLatencyRecorder{
		ServiceFunc: func(o meta.Object) []*object.Service {
			return dns.SvcIndex(object.ServiceKey(o.GetLabels()[discovery.LabelServiceName], o.GetNamespace()))
		},
	}
}

func podIPIndexFunc(obj interface{}) ([]string, error) {
	p, ok := obj.(*object.Pod)
	if !ok {
		return nil, errObj
	}
	return []string{p.PodIP}, nil
}

func svcIPIndexFunc(obj interface{}) ([]string, error) {
	svc, ok := obj.(*object.Service)
	if !ok {
		return nil, errObj
	}
	idx := make([]string, len(svc.ClusterIPs))
	copy(idx, svc.ClusterIPs)
	return idx, nil
}

func svcExtIPIndexFunc(obj interface{}) ([]string, error) {
	svc, ok := obj.(*object.Service)
	if !ok {
		return nil, errObj
	}
	idx := make([]string, len(svc.ExternalIPs))
	copy(idx, svc.ExternalIPs)
	return idx, nil
}

func svcNameNamespaceIndexFunc(obj interface{}) ([]string, error) {
	s, ok := obj.(*object.Service)
	if !ok {
		return nil, errObj
	}
	return []string{s.Index}, nil
}

func gatewayNameNamespaceIndexFunc(obj interface{}) ([]string, error) {
	s, ok := obj.(*object.Gateway)
	if !ok {
		return nil, errObj
	}
	return []string{s.Index}, nil
}

func gatewayAddressIndexFunc(obj interface{}) ([]string, error) {
	gateway, ok := obj.(*object.Gateway)
	if !ok {
		return nil, errObj
	}
	idx := []string{}
	for _, addr := range gateway.Status.Addresses {
		// find all of the addresses that are IP addresses
		if *addr.Type != gtwapi.IPAddressType {
			continue
		}

		idx = append(idx, addr.Value)
	}
	return idx, nil
}

func epNameNamespaceIndexFunc(obj interface{}) ([]string, error) {
	s, ok := obj.(*object.Endpoints)
	if !ok {
		return nil, errObj
	}
	return []string{s.Index}, nil
}

func epIPIndexFunc(obj interface{}) ([]string, error) {
	ep, ok := obj.(*object.Endpoints)
	if !ok {
		return nil, errObj
	}
	return ep.IndexIP, nil
}

func setupGatewayController(ctx context.Context, c kubernetes.Interface, dns *dnsControl) {
	var stop chan struct{}
	reh := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			log.Debug("Got a new CRD")
			crd, ok := obj.(*object.CustomResourceDefinition)
			if !ok {
				log.Errorf("Expected CustomResourceDefinition but got %T", obj)
				return
			}

			//TODO: Maybe we need to wait for a specific gatewayclass?
			if crd.Group != "gateway.networking.k8s.io" || crd.Name != kubeServiceCRDName {
				log.Debugf("Ignoring CustomResourceDefinition %s/%s because it is not a k8s gateway", crd.Group, crd.Name)
				return
			}

			// This is the gateway CRD; start the controller
			stop = make(chan struct{}) // always re-init the stop chan since it could've been closed after a previous controller run
			if !dns.gatewayControllerStarted.Load() {
				dns.gatewayLister, dns.gatewayController = object.NewIndexerInformer(
					&cache.ListWatch{
						ListFunc:  gatewayListFunc(ctx, dns.gtwClient, api.NamespaceAll, dns.selector),
						WatchFunc: gatewayWatchFunc(ctx, dns.gtwClient, api.NamespaceAll, dns.selector),
					},
					&gtwapi.Gateway{},
					cache.ResourceEventHandlerFuncs{AddFunc: dns.Add, UpdateFunc: dns.Update, DeleteFunc: dns.Delete}, // should match service handling
					cache.Indexers{gatewayNameNamespaceIndex: gatewayNameNamespaceIndexFunc, gatewayAddressIndex: gatewayAddressIndexFunc},
					object.DefaultProcessor(object.ToGateway, nil),
				)
				dns.gatewayControllerStarted.Store(true)
				go dns.gatewayController.Run(stop)
			}
		},
		DeleteFunc: func(obj interface{}) {
			crd, ok := obj.(*object.CustomResourceDefinition)
			if !ok {
				log.Errorf("Expected object.CustomResourceDefinition but got %T", obj)
				return
			}

			//TODO: Maybe we need to wait for a specific gatewayclass?
			if crd.Group != "gateway.networking.k8s.io" || crd.Name != kubeServiceCRDName {
				log.Debugf("Ignoring CustomResourceDefinition %s/%s because it is not a k8s gateway", crd.Group, crd.Name)
				return
			}

			// The gateway CRD was deleted; stop the gateway controller
			if dns.gatewayControllerStarted.Load() {
				// If the controller is running, stop it
				close(stop)
				dns.gatewayControllerStarted.Store(false)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			// TODO: figure out if we actually care about this?
			// The only time we potentially do is when the gwapi gets upgraded
		},
	}
	dns.crdLister, dns.crdController = object.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc:  crdListFunc(ctx, dns.crdClient),
			WatchFunc: crdWatchFunc(ctx, dns.crdClient),
		},
		&apiextensionsv1.CustomResourceDefinition{},
		reh,
		cache.Indexers{},
		object.DefaultProcessor(object.ToCustomResourceDefinition, nil),
	)

	go dns.crdController.Run(dns.stopCh)
}

func serviceListFunc(ctx context.Context, c kubernetes.Interface, ns string, s labels.Selector) func(meta.ListOptions) (runtime.Object, error) {
	return func(opts meta.ListOptions) (runtime.Object, error) {
		if s != nil {
			opts.LabelSelector = s.String()
		}
		return c.CoreV1().Services(ns).List(ctx, opts)
	}
}

func gatewayListFunc(ctx context.Context, c gtwapiclientset.Interface, ns string, s labels.Selector) func(meta.ListOptions) (runtime.Object, error) {
	return func(opts meta.ListOptions) (runtime.Object, error) {
		if s != nil {
			opts.LabelSelector = s.String()
		}
		return c.GatewayV1().Gateways(ns).List(ctx, opts)
	}
}

func crdListFunc(ctx context.Context, c crdclientset.Interface) func(meta.ListOptions) (runtime.Object, error) {
	return func(opts meta.ListOptions) (runtime.Object, error) {
		return c.ApiextensionsV1().CustomResourceDefinitions().List(ctx, opts)
	}
}

func podListFunc(ctx context.Context, c kubernetes.Interface, ns string, s labels.Selector) func(meta.ListOptions) (runtime.Object, error) {
	return func(opts meta.ListOptions) (runtime.Object, error) {
		if s != nil {
			opts.LabelSelector = s.String()
		}
		if len(opts.FieldSelector) > 0 {
			opts.FieldSelector = opts.FieldSelector + ","
		}
		opts.FieldSelector = opts.FieldSelector + "status.phase!=Succeeded,status.phase!=Failed,status.phase!=Unknown"
		return c.CoreV1().Pods(ns).List(ctx, opts)
	}
}

func endpointSliceListFunc(ctx context.Context, c kubernetes.Interface, ns string, s labels.Selector) func(meta.ListOptions) (runtime.Object, error) {
	return func(opts meta.ListOptions) (runtime.Object, error) {
		if s != nil {
			opts.LabelSelector = s.String()
		}
		return c.DiscoveryV1().EndpointSlices(ns).List(ctx, opts)
	}
}

func namespaceListFunc(ctx context.Context, c kubernetes.Interface, s labels.Selector) func(meta.ListOptions) (runtime.Object, error) {
	return func(opts meta.ListOptions) (runtime.Object, error) {
		if s != nil {
			opts.LabelSelector = s.String()
		}
		return c.CoreV1().Namespaces().List(ctx, opts)
	}
}

func serviceWatchFunc(ctx context.Context, c kubernetes.Interface, ns string, s labels.Selector) func(options meta.ListOptions) (watch.Interface, error) {
	return func(options meta.ListOptions) (watch.Interface, error) {
		if s != nil {
			options.LabelSelector = s.String()
		}
		return c.CoreV1().Services(ns).Watch(ctx, options)
	}
}

func crdWatchFunc(ctx context.Context, c crdclientset.Interface) func(options meta.ListOptions) (watch.Interface, error) {
	return func(options meta.ListOptions) (watch.Interface, error) {
		return c.ApiextensionsV1().CustomResourceDefinitions().Watch(ctx, options)
	}
}

func gatewayWatchFunc(ctx context.Context, c gtwapiclientset.Interface, ns string, s labels.Selector) func(options meta.ListOptions) (watch.Interface, error) {
	return func(options meta.ListOptions) (watch.Interface, error) {
		if s != nil {
			options.LabelSelector = s.String()
		}

		return c.GatewayV1().Gateways(ns).Watch(ctx, options)
	}
}

func podWatchFunc(ctx context.Context, c kubernetes.Interface, ns string, s labels.Selector) func(options meta.ListOptions) (watch.Interface, error) {
	return func(options meta.ListOptions) (watch.Interface, error) {
		if s != nil {
			options.LabelSelector = s.String()
		}
		if len(options.FieldSelector) > 0 {
			options.FieldSelector = options.FieldSelector + ","
		}
		options.FieldSelector = options.FieldSelector + "status.phase!=Succeeded,status.phase!=Failed,status.phase!=Unknown"
		return c.CoreV1().Pods(ns).Watch(ctx, options)
	}
}

func endpointSliceWatchFunc(ctx context.Context, c kubernetes.Interface, ns string, s labels.Selector) func(options meta.ListOptions) (watch.Interface, error) {
	return func(options meta.ListOptions) (watch.Interface, error) {
		if s != nil {
			options.LabelSelector = s.String()
		}
		return c.DiscoveryV1().EndpointSlices(ns).Watch(ctx, options)
	}
}

func namespaceWatchFunc(ctx context.Context, c kubernetes.Interface, s labels.Selector) func(options meta.ListOptions) (watch.Interface, error) {
	return func(options meta.ListOptions) (watch.Interface, error) {
		if s != nil {
			options.LabelSelector = s.String()
		}
		return c.CoreV1().Namespaces().Watch(ctx, options)
	}
}

// Stop stops the  controller.
func (dns *dnsControl) Stop() error {
	dns.stopLock.Lock()
	defer dns.stopLock.Unlock()

	// Only try draining the workqueue if we haven't already.
	if !dns.shutdown {
		close(dns.stopCh)
		dns.shutdown = true

		return nil
	}

	return fmt.Errorf("shutdown already in progress")
}

// Run starts the controller.
func (dns *dnsControl) Run() {
	go dns.svcController.Run(dns.stopCh)
	if dns.epController != nil {
		go func() {
			dns.epController.Run(dns.stopCh)
		}()
	}
	if dns.podController != nil {
		go dns.podController.Run(dns.stopCh)
	}
	go dns.nsController.Run(dns.stopCh)
	<-dns.stopCh
}

// HasSynced calls on all controllers.
func (dns *dnsControl) HasSynced() bool {
	a := dns.svcController.HasSynced()
	b := true
	if dns.epController != nil {
		b = dns.epController.HasSynced()
	}
	c := true
	if dns.podController != nil {
		c = dns.podController.HasSynced()
	}
	d := dns.nsController.HasSynced()

	if dns.gatewayControllerStarted.Load() {
		e := dns.gatewayController.HasSynced()
		return a && b && c && d && e
	}
	return a && b && c && d
}

func (dns *dnsControl) ServiceList() (svcs []*object.Service) {
	os := dns.svcLister.List()
	for _, o := range os {
		s, ok := o.(*object.Service)
		if !ok {
			continue
		}
		svcs = append(svcs, s)
	}
	return svcs
}

func (dns *dnsControl) GatewayList() (gateways []*object.Gateway) {
	os := dns.gatewayLister.List()
	for _, o := range os {
		gw, ok := o.(*object.Gateway)
		if !ok {
			continue
		}
		gateways = append(gateways, gw)
	}
	return gateways
}

func (dns *dnsControl) EndpointsList() (eps []*object.Endpoints) {
	os := dns.epLister.List()
	for _, o := range os {
		ep, ok := o.(*object.Endpoints)
		if !ok {
			continue
		}
		eps = append(eps, ep)
	}
	return eps
}

func (dns *dnsControl) PodIndex(ip string) (pods []*object.Pod) {
	os, err := dns.podLister.ByIndex(podIPIndex, ip)
	if err != nil {
		return nil
	}
	for _, o := range os {
		p, ok := o.(*object.Pod)
		if !ok {
			continue
		}
		pods = append(pods, p)
	}
	return pods
}

func (dns *dnsControl) SvcIndex(idx string) (svcs []*object.Service) {
	os, err := dns.svcLister.ByIndex(svcNameNamespaceIndex, idx)
	if err != nil {
		return nil
	}
	for _, o := range os {
		s, ok := o.(*object.Service)
		if !ok {
			continue
		}
		svcs = append(svcs, s)
	}
	return svcs
}

func (dns *dnsControl) GatewayIndex(idx string) (gateways []*object.Gateway) {
	if !dns.readGatewayAPI || dns.gatewayLister == nil {
		return nil
	}
	os, err := dns.gatewayLister.ByIndex(gatewayNameNamespaceIndex, idx)
	if err != nil {
		return nil
	}
	for _, o := range os {
		gw, ok := o.(*object.Gateway)
		if !ok {
			continue
		}
		gateways = append(gateways, gw)
	}
	return gateways
}

func (dns *dnsControl) GatewayAddressIndex(ip string) (gateways []*object.Gateway) {
	if !dns.readGatewayAPI || dns.gatewayLister == nil {
		return nil
	}
	log.Debugf("Looking up gateway for address %s", ip)
	os, err := dns.gatewayLister.ByIndex(gatewayAddressIndex, ip)
	if err != nil {
		return nil
	}
	for _, o := range os {
		gw, ok := o.(*object.Gateway)
		if !ok {
			continue
		}
		gateways = append(gateways, gw)
	}
	return gateways
}

func (dns *dnsControl) SvcIndexReverse(ip string) (svcs []*object.Service) {
	os, err := dns.svcLister.ByIndex(svcIPIndex, ip)
	if err != nil {
		return nil
	}

	for _, o := range os {
		s, ok := o.(*object.Service)
		if !ok {
			continue
		}
		svcs = append(svcs, s)
	}
	return svcs
}

func (dns *dnsControl) SvcExtIndexReverse(ip string) (svcs []*object.Service) {
	os, err := dns.svcLister.ByIndex(svcExtIPIndex, ip)
	if err != nil {
		return nil
	}

	for _, o := range os {
		s, ok := o.(*object.Service)
		if !ok {
			continue
		}
		svcs = append(svcs, s)
	}
	return svcs
}

func (dns *dnsControl) EpIndex(idx string) (ep []*object.Endpoints) {
	os, err := dns.epLister.ByIndex(epNameNamespaceIndex, idx)
	if err != nil {
		return nil
	}
	for _, o := range os {
		e, ok := o.(*object.Endpoints)
		if !ok {
			continue
		}
		ep = append(ep, e)
	}
	return ep
}

func (dns *dnsControl) EpIndexReverse(ip string) (ep []*object.Endpoints) {
	os, err := dns.epLister.ByIndex(epIPIndex, ip)
	if err != nil {
		return nil
	}
	for _, o := range os {
		e, ok := o.(*object.Endpoints)
		if !ok {
			continue
		}
		ep = append(ep, e)
	}
	return ep
}

// GetNodeByName return the node by name. If nothing is found an error is
// returned. This query causes a round trip to the k8s API server, so use
// sparingly. Currently, this is only used for Federation.
func (dns *dnsControl) GetNodeByName(ctx context.Context, name string) (*api.Node, error) {
	v1node, err := dns.client.CoreV1().Nodes().Get(ctx, name, meta.GetOptions{})
	return v1node, err
}

// GetNamespaceByName returns the namespace by name. If nothing is found an error is returned.
func (dns *dnsControl) GetNamespaceByName(name string) (*object.Namespace, error) {
	o, exists, err := dns.nsLister.GetByKey(name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("namespace not found")
	}
	ns, ok := o.(*object.Namespace)
	if !ok {
		return nil, fmt.Errorf("found key but not namespace")
	}
	return ns, nil
}

func (dns *dnsControl) Add(obj interface{})               { dns.updateModified() }
func (dns *dnsControl) Delete(obj interface{})            { dns.updateModified() }
func (dns *dnsControl) Update(oldObj, newObj interface{}) { dns.detectChanges(oldObj, newObj) }

// detectChanges detects changes in objects, and updates the modified timestamp
func (dns *dnsControl) detectChanges(oldObj, newObj interface{}) {
	// If both objects have the same resource version, they are identical.
	if newObj != nil && oldObj != nil && (oldObj.(meta.Object).GetResourceVersion() == newObj.(meta.Object).GetResourceVersion()) {
		return
	}
	obj := newObj
	if obj == nil {
		obj = oldObj
	}
	switch ob := obj.(type) {
	case *object.Service:
		imod, emod := serviceModified(oldObj, newObj)
		if imod {
			dns.updateModified()
		}
		if emod {
			dns.updateExtModified()
		}
	case *object.Gateway:
		imod, _ := gatewayModified(oldObj, newObj)
		if imod {
			dns.updateModified()
		}
	case *object.Pod:
		dns.updateModified()
	case *object.Endpoints:
		if !endpointsEquivalent(oldObj.(*object.Endpoints), newObj.(*object.Endpoints)) {
			dns.updateModified()
		}
	default:
		log.Warningf("Updates for %T not supported.", ob)
	}
}

// subsetsEquivalent checks if two endpoint subsets are significantly equivalent
// I.e. that they have the same ready addresses, host names, ports (including protocol
// and service names for SRV)
func subsetsEquivalent(sa, sb object.EndpointSubset) bool {
	if len(sa.Addresses) != len(sb.Addresses) {
		return false
	}
	if len(sa.Ports) != len(sb.Ports) {
		return false
	}

	// in Addresses and Ports, we should be able to rely on
	// these being sorted and able to be compared
	// they are supposed to be in a canonical format
	for addr, aaddr := range sa.Addresses {
		baddr := sb.Addresses[addr]
		if aaddr.IP != baddr.IP {
			return false
		}
		if aaddr.Hostname != baddr.Hostname {
			return false
		}
	}

	for port, aport := range sa.Ports {
		bport := sb.Ports[port]
		if aport.Name != bport.Name {
			return false
		}
		if aport.Port != bport.Port {
			return false
		}
		if aport.Protocol != bport.Protocol {
			return false
		}
	}
	return true
}

// endpointsEquivalent checks if the update to an endpoint is something
// that matters to us or if they are effectively equivalent.
func endpointsEquivalent(a, b *object.Endpoints) bool {
	if a == nil || b == nil {
		return false
	}

	if len(a.Subsets) != len(b.Subsets) {
		return false
	}

	// we should be able to rely on
	// these being sorted and able to be compared
	// they are supposed to be in a canonical format
	for i, sa := range a.Subsets {
		sb := b.Subsets[i]
		if !subsetsEquivalent(sa, sb) {
			return false
		}
	}
	return true
}

// gatewayModified chcks the gateways passed for changes that result to changes
// to internal and/or external records. It returns two booleans, one for internal
// record changes, and a second for external record changes
func gatewayModified(oldObj, newObj interface{}) (intGtw, extGtw bool) {
	extGtw = false // TODO: implement external gateway records for this POC

	newGtw := newObj.(*object.Gateway)
	oldGtw := oldObj.(*object.Gateway)

	if len(oldGtw.Status.Addresses) != len(newGtw.Status.Addresses) {
		intGtw = true
	}

	if intGtw {
		return
	}

	if len(oldGtw.Listeners) != len(newGtw.Listeners) {
		intGtw = true
	}

	if len(oldGtw.Status.Listeners) != len(newGtw.Status.Listeners) {
		intGtw = true
	}

	for i := range oldGtw.Listeners {
		if oldGtw.Listeners[i].Port != newGtw.Listeners[i].Port {
			intGtw = true
			break
		}
		if oldGtw.Listeners[i].Protocol != newGtw.Listeners[i].Protocol {
			intGtw = true
			break
		}
		if oldGtw.Listeners[i].Name != newGtw.Listeners[i].Name {
			intGtw = true
			break
		}
		if oldGtw.Listeners[i].Hostname != newGtw.Listeners[i].Hostname {
			intGtw = true
			break
		}
		listenerName := oldGtw.Listeners[i].Name
		if len(oldGtw.ListenerStatusMap[listenerName]) != len(newGtw.ListenerStatusMap[listenerName]) {
			intGtw = true
			break
		}
		for t, c := range oldGtw.ListenerStatusMap[listenerName] {
			if c != newGtw.ListenerStatusMap[listenerName][t] {
				intGtw = true
				break
			}
		}
	}
	return
}

// serviceModified checks the services passed for changes that result in changes
// to internal and or external records. It returns two booleans, one for internal
// record changes, and a second for external record changes
func serviceModified(oldObj, newObj interface{}) (intSvc, extSvc bool) {
	if oldObj != nil && newObj == nil {
		// deleted service only modifies external zone records if it had external ips
		return true, len(oldObj.(*object.Service).ExternalIPs) > 0
	}

	if oldObj == nil && newObj != nil {
		// added service only modifies external zone records if it has external ips
		return true, len(newObj.(*object.Service).ExternalIPs) > 0
	}

	newSvc := newObj.(*object.Service)
	oldSvc := oldObj.(*object.Service)

	// External IPs are mutable, affecting external zone records
	if len(oldSvc.ExternalIPs) != len(newSvc.ExternalIPs) {
		extSvc = true
	} else {
		for i := range oldSvc.ExternalIPs {
			if oldSvc.ExternalIPs[i] != newSvc.ExternalIPs[i] {
				extSvc = true
				break
			}
		}
	}

	// ExternalName is mutable, affecting internal zone records
	intSvc = oldSvc.ExternalName != newSvc.ExternalName

	if intSvc && extSvc {
		return intSvc, extSvc
	}

	// All Port fields are mutable, affecting both internal/external zone records
	if len(oldSvc.Ports) != len(newSvc.Ports) {
		return true, true
	}
	for i := range oldSvc.Ports {
		if oldSvc.Ports[i].Name != newSvc.Ports[i].Name {
			return true, true
		}
		if oldSvc.Ports[i].Port != newSvc.Ports[i].Port {
			return true, true
		}
		if oldSvc.Ports[i].Protocol != newSvc.Ports[i].Protocol {
			return true, true
		}
	}

	return intSvc, extSvc
}

func (dns *dnsControl) Modified(external bool) int64 {
	if external {
		return atomic.LoadInt64(&dns.extModified)
	}
	return atomic.LoadInt64(&dns.modified)
}

// updateModified set dns.modified to the current time.
func (dns *dnsControl) updateModified() {
	unix := time.Now().Unix()
	atomic.StoreInt64(&dns.modified, unix)
}

// updateExtModified set dns.extModified to the current time.
func (dns *dnsControl) updateExtModified() {
	unix := time.Now().Unix()
	atomic.StoreInt64(&dns.extModified, unix)
}

var errObj = errors.New("obj was not of the correct type")
