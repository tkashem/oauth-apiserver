package install

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	userv1 "github.com/openshift/api/user/v1"
	userapiv1 "github.com/openshift/oauth-apiserver/pkg/user/apis/user/v1"
)

// Install registers the API group and adds types to a scheme
func Install(scheme *runtime.Scheme) {
	utilruntime.Must(userapiv1.Install(scheme))
	utilruntime.Must(scheme.SetVersionPriority(userv1.GroupVersion))
}
