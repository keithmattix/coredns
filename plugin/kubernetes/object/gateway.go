package object

import (
	"fmt"

	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	gtwapi "sigs.k8s.io/gateway-api/apis/v1"
)

// Gateway is a stripped down api.Gateway with only the items we need for CoreDNS.
type Gateway struct {
	// Don't add new fields to this struct without talking to the CoreDNS maintainers.
	Version      string
	Name         string
	Namespace    string
	Index        string
	GatewayClass gtwapi.ObjectName
	Addresses    []gtwapi.GatewayAddress
	Listeners    []gtwapi.Listener
	Status       gtwapi.GatewayStatus

	*Empty
}

// GatewayKey returns a string using for the index.
func GatewayKey(name, namespace string) string { return name + "." + namespace }

// ToGateway converts an api.Service to a *Service.
func ToGateway(obj meta.Object) (meta.Object, error) {
	gateway, ok := obj.(*gtwapi.Gateway)
	if !ok {
		return nil, fmt.Errorf("unexpected object %v", obj)
	}
	s := &Gateway{
		Version:      gateway.GetResourceVersion(),
		Name:         gateway.GetName(),
		Namespace:    gateway.GetNamespace(),
		Index:        GatewayKey(gateway.GetName(), gateway.GetNamespace()),
		GatewayClass: gateway.Spec.GatewayClassName,
		Addresses:    gateway.Spec.Addresses,
		Listeners:    gateway.Spec.Listeners,
		Status:       gateway.Status,
	}

	return s, nil
}

var _ runtime.Object = &Gateway{}

// DeepCopyObject implements the ObjectKind interface.
func (s *Gateway) DeepCopyObject() runtime.Object {
	s1 := &Gateway{
		Version:      s.Version,
		Name:         s.Name,
		Namespace:    s.Namespace,
		GatewayClass: s.GatewayClass,
		Addresses:    make([]gtwapi.GatewayAddress, len(s.Addresses)),
		Listeners:    make([]gtwapi.Listener, len(s.Listeners)),
	}
	copy(s1.Addresses, s.Addresses)
	copy(s1.Listeners, s.Listeners)
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
