package apiserver

import (
	"fmt"
	"sync"

	oauthinstall "github.com/openshift/oauth-apiserver/pkg/oauth/apis/oauth/install"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"

	oauthapiv1 "github.com/openshift/api/oauth/v1"
	oauthclient "github.com/openshift/client-go/oauth/clientset/versioned/typed/oauth/v1"
	routeclient "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	"github.com/openshift/library-go/pkg/oauth/oauthserviceaccountclient"
	accesstokenetcd "github.com/openshift/oauth-apiserver/pkg/oauth/apiserver/registry/oauthaccesstoken/etcd"
	authorizetokenetcd "github.com/openshift/oauth-apiserver/pkg/oauth/apiserver/registry/oauthauthorizetoken/etcd"
	clientetcd "github.com/openshift/oauth-apiserver/pkg/oauth/apiserver/registry/oauthclient/etcd"
	clientauthetcd "github.com/openshift/oauth-apiserver/pkg/oauth/apiserver/registry/oauthclientauthorization/etcd"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme)
)

func init() {
	metav1.AddToGroupVersion(scheme, metav1.SchemeGroupVersion)
	metav1.AddToGroupVersion(scheme, schema.GroupVersion{Group: "", Version: "v1"})
	oauthinstall.Install(scheme)
}

type ExtraConfig struct {
	ServiceAccountMethod string

	makeV1Storage sync.Once
	v1Storage     map[string]rest.Storage
	v1StorageErr  error
}
type OAuthAPIServerConfig struct {
	GenericConfig *genericapiserver.RecommendedConfig
	ExtraConfig   ExtraConfig
}

type OAuthAPIServer struct {
	GenericAPIServer *genericapiserver.GenericAPIServer
}

type completedConfig struct {
	GenericConfig             genericapiserver.CompletedConfig
	ExtraConfig               *ExtraConfig
	kubeAPIServerClientConfig *restclient.Config
}

type CompletedConfig struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedConfig
}

// Complete fills in any fields not set that are required to have valid data. It's mutating the receiver.
func (c *OAuthAPIServerConfig) Complete() completedConfig {
	cfg := completedConfig{
		GenericConfig:             c.GenericConfig.Complete(),
		ExtraConfig:               &c.ExtraConfig,
		kubeAPIServerClientConfig: c.GenericConfig.ClientConfig,
	}

	return cfg
}

// New returns a new instance of OAuthAPIServer from the given config.
func (c completedConfig) New(delegationTarget genericapiserver.DelegationTarget) (*OAuthAPIServer, error) {
	genericServer, err := c.GenericConfig.New("oauth.openshift.io-apiserver", delegationTarget)
	if err != nil {
		return nil, err
	}

	s := &OAuthAPIServer{
		GenericAPIServer: genericServer,
	}

	v1Storage, err := c.V1RESTStorage()
	if err != nil {
		return nil, err
	}

	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(oauthapiv1.GroupName, scheme, metav1.ParameterCodec, codecs)
	apiGroupInfo.VersionedResourcesStorageMap[oauthapiv1.SchemeGroupVersion.Version] = v1Storage
	if err := s.GenericAPIServer.InstallAPIGroup(&apiGroupInfo); err != nil {
		return nil, err
	}

	return s, nil
}

func (c *completedConfig) V1RESTStorage() (map[string]rest.Storage, error) {
	c.ExtraConfig.makeV1Storage.Do(func() {
		c.ExtraConfig.v1Storage, c.ExtraConfig.v1StorageErr = c.newV1RESTStorage()
	})

	return c.ExtraConfig.v1Storage, c.ExtraConfig.v1StorageErr
}

func (c *completedConfig) newV1RESTStorage() (map[string]rest.Storage, error) {
	clientStorage, err := clientetcd.NewREST(c.GenericConfig.RESTOptionsGetter)
	if err != nil {
		return nil, fmt.Errorf("error building REST storage: %v", err)
	}

	// If OAuth is disabled, set the strategy to Deny
	saAccountGrantMethod := oauthapiv1.GrantHandlerDeny
	if len(c.ExtraConfig.ServiceAccountMethod) > 0 {
		// Otherwise, take the value provided in master-config.yaml
		saAccountGrantMethod = oauthapiv1.GrantHandlerType(c.ExtraConfig.ServiceAccountMethod)
	}

	oauthClient, err := oauthclient.NewForConfig(c.GenericConfig.LoopbackClientConfig)
	if err != nil {
		return nil, err
	}
	routeClient, err := routeclient.NewForConfig(c.kubeAPIServerClientConfig)
	if err != nil {
		return nil, err
	}
	coreV1Client, err := corev1.NewForConfig(c.kubeAPIServerClientConfig)
	if err != nil {
		return nil, err
	}

	combinedOAuthClientGetter := oauthserviceaccountclient.NewServiceAccountOAuthClientGetter(
		coreV1Client,
		coreV1Client,
		coreV1Client.Events(""),
		routeClient,
		oauthClient.OAuthClients(),
		saAccountGrantMethod,
	)
	authorizeTokenStorage, err := authorizetokenetcd.NewREST(c.GenericConfig.RESTOptionsGetter, combinedOAuthClientGetter)
	if err != nil {
		return nil, fmt.Errorf("error building REST storage: %v", err)
	}
	accessTokenStorage, err := accesstokenetcd.NewREST(c.GenericConfig.RESTOptionsGetter, combinedOAuthClientGetter)
	if err != nil {
		return nil, fmt.Errorf("error building REST storage: %v", err)
	}
	clientAuthorizationStorage, err := clientauthetcd.NewREST(c.GenericConfig.RESTOptionsGetter, combinedOAuthClientGetter)
	if err != nil {
		return nil, fmt.Errorf("error building REST storage: %v", err)
	}

	v1Storage := map[string]rest.Storage{}
	v1Storage["oAuthAuthorizeTokens"] = authorizeTokenStorage
	v1Storage["oAuthAccessTokens"] = accessTokenStorage
	v1Storage["oAuthClients"] = clientStorage
	v1Storage["oAuthClientAuthorizations"] = clientAuthorizationStorage
	return v1Storage, nil
}
