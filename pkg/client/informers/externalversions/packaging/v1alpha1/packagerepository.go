// Code generated by main. DO NOT EDIT.

package v1alpha1

import (
	"context"
	time "time"

	packagingv1alpha1 "carvel.dev/kapp-controller/pkg/apis/packaging/v1alpha1"
	versioned "carvel.dev/kapp-controller/pkg/client/clientset/versioned"
	internalinterfaces "carvel.dev/kapp-controller/pkg/client/informers/externalversions/internalinterfaces"
	v1alpha1 "carvel.dev/kapp-controller/pkg/client/listers/packaging/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// PackageRepositoryInformer provides access to a shared informer and lister for
// PackageRepositories.
type PackageRepositoryInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1alpha1.PackageRepositoryLister
}

type packageRepositoryInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
	namespace        string
}

// NewPackageRepositoryInformer constructs a new informer for PackageRepository type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewPackageRepositoryInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredPackageRepositoryInformer(client, namespace, resyncPeriod, indexers, nil)
}

// NewFilteredPackageRepositoryInformer constructs a new informer for PackageRepository type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredPackageRepositoryInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.PackagingV1alpha1().PackageRepositories(namespace).List(context.TODO(), options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.PackagingV1alpha1().PackageRepositories(namespace).Watch(context.TODO(), options)
			},
		},
		&packagingv1alpha1.PackageRepository{},
		resyncPeriod,
		indexers,
	)
}

func (f *packageRepositoryInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredPackageRepositoryInformer(client, f.namespace, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *packageRepositoryInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&packagingv1alpha1.PackageRepository{}, f.defaultInformer)
}

func (f *packageRepositoryInformer) Lister() v1alpha1.PackageRepositoryLister {
	return v1alpha1.NewPackageRepositoryLister(f.Informer().GetIndexer())
}
