package object

import (
	"fmt"

	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	gtwapi "sigs.k8s.io/gateway-api/apis/v1"
)

type ListenerConditions map[gtwapi.ListenerConditionType]meta.Condition

// Gateway is a stripped down api.Gateway with only the items we need for CoreDNS.
type Gateway struct {
	// Don't add new fields to this struct without talking to the CoreDNS maintainers.
	Version           string
	Name              string
	Namespace         string
	Index             string
	GatewayClass      gtwapi.ObjectName
	Addresses         []gtwapi.GatewayAddress
	Listeners         []gtwapi.Listener
	Status            gtwapi.GatewayStatus
	ListenerStatusMap map[gtwapi.SectionName]ListenerConditions
	*Empty
}

// GatewayKey returns a string using for the index.
func GatewayKey(name, namespace string) string {
	return name + "." + namespace
}

// ToGateway converts an api.Service to a *Service.
func ToGateway(obj meta.Object) (meta.Object, error) {
	gateway, ok := obj.(*gtwapi.Gateway)
	if !ok {
		return nil, fmt.Errorf("unexpected object %v", obj)
	}
	s := &Gateway{
		Version:           gateway.GetResourceVersion(),
		Name:              gateway.GetName(),
		Namespace:         gateway.GetNamespace(),
		Index:             GatewayKey(gateway.GetName(), gateway.GetNamespace()),
		GatewayClass:      gateway.Spec.GatewayClassName,
		Listeners:         gateway.Spec.Listeners,
		Status:            gateway.Status,
		ListenerStatusMap: make(map[gtwapi.SectionName]ListenerConditions, len(gateway.Status.Listeners)),
	}

	for _, l := range gateway.Status.Listeners {
		lc := make(ListenerConditions, len(l.Conditions))
		for _, c := range l.Conditions {
			lc[gtwapi.ListenerConditionType(c.Type)] = c
		}
		s.ListenerStatusMap[l.Name] = lc
	}

	return s, nil
}

var _ runtime.Object = &Gateway{}

// DeepCopyObject implements the ObjectKind interface.
func (s *Gateway) DeepCopyObject() runtime.Object {
	s1 := &Gateway{
		Version:           s.Version,
		Name:              s.Name,
		Namespace:         s.Namespace,
		GatewayClass:      s.GatewayClass,
		Addresses:         make([]gtwapi.GatewayAddress, len(s.Addresses)),
		Listeners:         make([]gtwapi.Listener, len(s.Listeners)),
		Status:            s.Status,
		ListenerStatusMap: make(map[gtwapi.SectionName]ListenerConditions, len(s.ListenerStatusMap)),
	}
	copy(s1.Addresses, s.Addresses)
	copy(s1.Listeners, s.Listeners)
	for k, v := range s.ListenerStatusMap {
		s1.ListenerStatusMap[k] = v
	}
	return s1
}

// GetNamespace implements the metav1.Object interface.
func (s *Gateway) GetNamespace() string { return s.Namespace }

// SetNamespace implements the metav1.Object interface.
func (s *Gateway) SetNamespace(namespace string) {}

// GetName implements the metav1.Object interface.
func (s *Gateway) GetName() string { return s.Name }

// SetName implements the metav1.Object interface.
func (s *Gateway) SetName(name string) {}

// GetResourceVersion implements the metav1.Object interface.
func (s *Gateway) GetResourceVersion() string { return s.Version }

// SetResourceVersion implements the metav1.Object interface.
func (s *Gateway) SetResourceVersion(version string) {}
