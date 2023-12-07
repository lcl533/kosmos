// Code generated by lister-gen. DO NOT EDIT.

package v1alpha1

import (
	v1alpha1 "github.com/kosmos.io/kosmos/pkg/apis/kosmos/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// PodConvertPolicyLister helps list PodConvertPolicies.
// All objects returned here must be treated as read-only.
type PodConvertPolicyLister interface {
	// List lists all PodConvertPolicies in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1alpha1.PodConvertPolicy, err error)
	// PodConvertPolicies returns an object that can list and get PodConvertPolicies.
	PodConvertPolicies(namespace string) PodConvertPolicyNamespaceLister
	PodConvertPolicyListerExpansion
}

// podConvertPolicyLister implements the PodConvertPolicyLister interface.
type podConvertPolicyLister struct {
	indexer cache.Indexer
}

// NewPodConvertPolicyLister returns a new PodConvertPolicyLister.
func NewPodConvertPolicyLister(indexer cache.Indexer) PodConvertPolicyLister {
	return &podConvertPolicyLister{indexer: indexer}
}

// List lists all PodConvertPolicies in the indexer.
func (s *podConvertPolicyLister) List(selector labels.Selector) (ret []*v1alpha1.PodConvertPolicy, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.PodConvertPolicy))
	})
	return ret, err
}

// PodConvertPolicies returns an object that can list and get PodConvertPolicies.
func (s *podConvertPolicyLister) PodConvertPolicies(namespace string) PodConvertPolicyNamespaceLister {
	return podConvertPolicyNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// PodConvertPolicyNamespaceLister helps list and get PodConvertPolicies.
// All objects returned here must be treated as read-only.
type PodConvertPolicyNamespaceLister interface {
	// List lists all PodConvertPolicies in the indexer for a given namespace.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1alpha1.PodConvertPolicy, err error)
	// Get retrieves the PodConvertPolicy from the indexer for a given namespace and name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1alpha1.PodConvertPolicy, error)
	PodConvertPolicyNamespaceListerExpansion
}

// podConvertPolicyNamespaceLister implements the PodConvertPolicyNamespaceLister
// interface.
type podConvertPolicyNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all PodConvertPolicies in the indexer for a given namespace.
func (s podConvertPolicyNamespaceLister) List(selector labels.Selector) (ret []*v1alpha1.PodConvertPolicy, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.PodConvertPolicy))
	})
	return ret, err
}

// Get retrieves the PodConvertPolicy from the indexer for a given namespace and name.
func (s podConvertPolicyNamespaceLister) Get(name string) (*v1alpha1.PodConvertPolicy, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1alpha1.Resource("podconvertpolicy"), name)
	}
	return obj.(*v1alpha1.PodConvertPolicy), nil
}