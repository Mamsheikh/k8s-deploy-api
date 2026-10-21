package kube

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	ContextName = "docker-desktop"
	Namespace   = "deploy-api"
)

func Load(path string) (*kubernetes.Clientset, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = path
	overrides := &clientcmd.ConfigOverrides{CurrentContext: ContextName}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig for context %q: %w", ContextName, err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client for context %q: %w", ContextName, err)
	}
	return client, nil
}

func Check(ctx context.Context, client *kubernetes.Clientset) (version.Info, error) {
	var info version.Info
	data, err := client.Discovery().RESTClient().Get().AbsPath("/version").Do(ctx).Raw()
	if err != nil {
		return info, fmt.Errorf("get Kubernetes server version: %w", err)
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, fmt.Errorf("decode Kubernetes server version: %w", err)
	}
	if _, err := client.CoreV1().Namespaces().Get(ctx, Namespace, metav1.GetOptions{}); err != nil {
		return info, fmt.Errorf("get namespace %q: %w", Namespace, err)
	}
	return info, nil
}
