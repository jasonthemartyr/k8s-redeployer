package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// assuming kubeconfig was setup via kubectl
// add k8s contect to this const
const k8sContext = ""

type DeploymentDetails struct {
	Name string
	Pods []PodDetails
}
type PodDetails struct {
	Name        string
	RestartedOn string
}

func main() {
	logger := NewLogger()
	k8sClient, err := NewK8sClient(logger)
	if err != nil {
		logger.With("error", err).Error("failed to initialize NewK8sClient()")
		os.Exit(1)
	}
	logger.With("context", k8sContext).Info("k8s client created and context sucessfully loaded")
	d, err := redeployDatabasePods(*k8sClient)
	if err != nil {
		logger.With("error", err).Error("failed to redeploy pods")
		os.Exit(1)
	}

	logger.With("deployment", d.Name).Info("sucessfully patched deployment")
	for _, dd := range d.Pods {
		logger.With("pod", dd.Name, "restarted_on", dd.RestartedOn).Info("sucessfully redeployed pod")
	}
}

// NewLogger() iniatlizes and returns a slog logger
func NewLogger() *slog.Logger {
	l := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	if l == nil {
		log.Panic("logger failed to initialize")
	}
	return l
}

// NewK8sClient takes slog logger as an input param and returns k8s client and nil if sucessful.
// If an error is encourted it returns a nil client and the error.
func NewK8sClient(logger *slog.Logger) (*kubernetes.Clientset, error) {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		logger.Info("kubeconfig found in default location: '$HOME/.kube/config'")
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		logger.Info("kubeconfig not found. creating it in '$HOME/.kube/config'")
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	config, err := clientcmd.LoadFromFile(*kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig from file %s", err.Error())
	}

	if _, exists := config.Contexts[k8sContext]; !exists {
		return nil, fmt.Errorf("kubecontext does not exist in the kubeconfig %s", k8sContext)
	}

	config.CurrentContext = k8sContext
	K8sConfig, err := clientcmd.NewDefaultClientConfig(*config, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(K8sConfig)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// redeployDatabasePods takes k8s client as an input param and returns a DeploymentDetails struct and nil if sucessful.
// If an error is encourted it returns an empty DeploymentDetails struct and the error.
func redeployDatabasePods(k kubernetes.Clientset) (DeploymentDetails, error) {
	var output DeploymentDetails
	deployments, err := k.AppsV1().Deployments("").List(context.TODO(), metav1.ListOptions{}) // searching all namespace
	if err != nil {
		return output, fmt.Errorf("failed to list deployment: %s", err.Error())
	}
	deploymentsMap := make(map[string]string)
	for _, d := range deployments.Items {
		if strings.Contains(d.Name, "database") {
			deploymentsMap[d.Name] = metav1.FormatLabelSelector(d.Spec.Selector) // FormatLabelSelector converts to a plain string per go docs
		}
	}

	for deploymentName, label := range deploymentsMap {
		pods, err := k.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{LabelSelector: label}) // searching all namespace
		if err != nil {
			return output, fmt.Errorf("failed to list pods for deployment %s: %s", deploymentName, err.Error())
		}
		output.Name = deploymentName
		for _, pod := range pods.Items {
			timeStamp := time.Now().UTC().Format(time.RFC3339)
			patchData := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"restarted_at":"%s"}}}}}`, timeStamp)
			_, err := k.AppsV1().Deployments(pod.Namespace).Patch(context.TODO(), deploymentName, types.StrategicMergePatchType, []byte(patchData), metav1.PatchOptions{})
			if err != nil {
				return output, fmt.Errorf("failed to patch deployment %s for pod %s: %s", deploymentName, pod.Name, err.Error())
			}
			podDetail := PodDetails{
				Name:        pod.Name,
				RestartedOn: timeStamp,
			}
			output.Pods = append(output.Pods, podDetail)
		}

	}
	return output, nil
}
