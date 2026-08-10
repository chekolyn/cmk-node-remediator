package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"

	"crusoe-cmk-node-remediation/internal/actions"
	"crusoe-cmk-node-remediation/internal/config"
	"crusoe-cmk-node-remediation/internal/crusoe"
	"crusoe-cmk-node-remediation/internal/discovery"
	"crusoe-cmk-node-remediation/internal/guardrails"
	"crusoe-cmk-node-remediation/internal/k8s"
	"crusoe-cmk-node-remediation/internal/lifecycle"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.Println("node-remediation starting")

	// 1. Load config from mounted ConfigMap
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "/etc/node-remediation/config.yaml"
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("failed to read config file %s: %v", configPath, err)
	}

	cfg, err := config.LoadFromYAML(configData)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("config validation failed: %v", err)
	}

	log.Printf("config loaded: cordonDays=%d, actionDays=%d, action=%s, dryRun=%v",
		cfg.CordonDays, cfg.ActionDays, cfg.Action.Type, cfg.DryRun)

	// 2. Load Crusoe API credentials from mounted Secret
	apiKey := os.Getenv("CRUSOE_API_KEY")
	apiURL := os.Getenv("CRUSOE_API_URL")
	if apiKey == "" || apiURL == "" {
		log.Fatalf("CRUSOE_API_KEY and CRUSOE_API_URL must be set")
	}
	projectID := cfg.CrusoeProjectID
	if projectID == "" {
		log.Fatalf("crusoeProjectId must be set in config")
	}

	// 3. Create K8s client
	k8sClientset, _, err := createK8sClient()
	if err != nil {
		log.Fatalf("failed to create K8s client: %v", err)
	}

	k8sClient := k8s.NewClient(k8sClientset, nil)

	// 4. Create Crusoe API client
	crusoeClient := crusoe.NewClient(apiURL, apiKey, projectID)

	// 5. Create step factory
	stepFactory := actions.NewFactory(k8sClient, crusoeClient)

	// 6. Create drain manager
	drainMgr := k8s.NewDrainManager(k8sClient, k8sClientset, cfg.DrainTimeout, cfg.ForceDrainAfterTimeout)

	// 7. Create guardrail checker
	guardChecker := guardrails.NewChecker(cfg.Guardrails)

	// 8. Create discoverer
	discoverer := discovery.NewDiscoverer(k8sClient, cfg)

	// 9. Create K8s Event recorder
	broadcaster := record.NewBroadcaster()
	broadcaster.StartLogging(log.Printf)
	broadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: k8sClientset.CoreV1().Events("")})
	recorder := broadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "node-remediation"})

	// 10. Create uptime evaluator (uses K8s client for stats summary)
	uptimeEval := &k8sUptimeEvaluator{k8sClient: k8sClient}

	// 11. Create lifecycle manager
	deps := lifecycle.Dependencies{
		K8sClient:    k8sClient,
		DrainMgr:     drainMgr,
		StepFactory:  stepFactory,
		UptimeEval:   uptimeEval,
		GuardChecker: guardChecker,
		Discoverer:   discoverer,
		Config:       cfg,
		Recorder:     recorder,
	}

	mgr := lifecycle.NewManager(deps)

	// 12. Run one pass (CronJob triggers a new pod each run)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	if err := mgr.Run(ctx); err != nil {
		log.Printf("lifecycle run failed: %v", err)
		os.Exit(1)
	}

	log.Println("node-remediation completed successfully")
}

// k8sUptimeEvaluator implements lifecycle.UptimeEvaluator using the K8s client.
type k8sUptimeEvaluator struct {
	k8sClient *k8s.Client
}

func (e *k8sUptimeEvaluator) GetNodeStartTime(ctx context.Context, nodeName string) (time.Time, error) {
	return e.k8sClient.GetNodeStartTime(ctx, nodeName)
}

func createK8sClient() (*kubernetes.Clientset, *rest.Config, error) {
	// Try in-cluster config first
	config, err := rest.InClusterConfig()
	if err == nil {
		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			return nil, nil, err
		}
		return clientset, config, nil
	}

	// Fall back to kubeconfig
	home := os.Getenv("HOME")
	if home == "" {
		home = "/root"
	}
	kubeconfigPath := filepath.Join(home, ".kube", "config")

	config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create K8s config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}
	return clientset, config, nil
}
