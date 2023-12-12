package object

import (
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// CustomResourceDefinition is a stripped down api.CustomResourceDefinition with only the items we need for CoreDNS.
type CustomResourceDefinition struct {
	// Don't add new fields to this struct without talking to the CoreDNS maintainers.
	Version string
	Group   string
	Kind    string
	Name    string

	*Empty
}

// ToCustomResourceDefinition returns a function that converts an api.CustomResourceDefinition to a *CustomResourceDefinition.
func ToCustomResourceDefinition(obj meta.Object) (meta.Object, error) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil, fmt.Errorf("unexpected object %v", obj)
	}
	c := &CustomResourceDefinition{
		Version: crd.GetResourceVersion(),
		Kind:    crd.Spec.Names.Kind,
		Group:   crd.Spec.Group,
		Name:    crd.Name,
	}
	*crd = apiextensionsv1.CustomResourceDefinition{}
	return c, nil
}

var _ runtime.Object = &CustomResourceDefinition{}

// DeepCopyObject implements the ObjectKind interface.
func (n *CustomResourceDefinition) DeepCopyObject() runtime.Object {
	n1 := &CustomResourceDefinition{
		Version: n.Version,
		Name:    n.Name,
		Kind:    n.Kind,
		Group:   n.Group,
	}
	return n1
}

// GetNamespace implements the metav1.Object interface.
func (n *CustomResourceDefinition) GetNamespace() string { return "" }

// SetNamespace implements the metav1.Object interface.
func (n *CustomResourceDefinition) SetNamespace(namespace string) {}

// GetCustomResourceDefinition implements the metav1.Object interface.
func (n *CustomResourceDefinition) GetCustomResourceDefinition() string { return "" }

// SetCustomResourceDefinition implements the metav1.Object interface.
func (n *CustomResourceDefinition) SetCustomResourceDefinition(namespace string) {}

// GetName implements the metav1.Object interface.
func (n *CustomResourceDefinition) GetName() string { return n.Name }

// SetName implements the metav1.Object interface.
func (n *CustomResourceDefinition) SetName(name string) {}

// GetResourceVersion implements the metav1.Object interface.
func (n *CustomResourceDefinition) GetResourceVersion() string { return n.Version }

// SetResourceVersion implements the metav1.Object interface.
func (n *CustomResourceDefinition) SetResourceVersion(version string) {}
