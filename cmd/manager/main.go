package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"crusoe-node-remediation/internal/actions"
	"crusoe-node-remediation/internal/config"
	"crusoe-node-remediation/internal/crusoe"
	"crusoe-node-remediation/internal/discovery"
	"crusoe-node-remediation/internal/guardrails"
	"crusoe-node-remediation/internal/k8s"
	"crusoe-node-remediation/internal/remediation"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	// --validate: load and validate config, then exit (for e2e/CI)
	validateOnly := flag.Bool("validate", false, "validate config and exit")
	flag.Parse()

	log.Println("crusoe-node-remediation starting")
	log.Println("────────────────────────────────────────────────────────────")

	// 1. Load config from mounted ConfigMap
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "/etc/crusoe-node-remediation/config.yaml"
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

	log.Printf("config: thresholds=(cordon=%s, remediation=%s, cooldown=%s), action=(type=%s, dryRun=%v), guardrails=(global=%d, perPool=%d), drain=(timeout=%s, force=%v)",
		cfg.CordonDuration(), cfg.RemediationThresholdDuration(), cfg.RemediationCooldownDuration(),
		cfg.Action.Type, cfg.DryRun,
		cfg.Guardrails.GlobalMaxCordoned, cfg.Guardrails.PerPoolMaxCordoned,
		cfg.DrainTimeout, cfg.ForceAfterEvictionFailure)

	// If --validate, exit here (config is valid)
	if *validateOnly {
		log.Println("config validation passed")
		os.Exit(0)
	}

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
	drainMgr := k8s.NewDrainManager(k8sClient, k8sClientset, cfg.DrainTimeout, cfg.ForceAfterEvictionFailure)

	// 7. Create guardrail checker
	guardChecker := guardrails.NewChecker(cfg.Guardrails)

	// 8. Create discoverer
	discoverer := discovery.NewDiscoverer(k8sClient, cfg)

	// 9. Create K8s Event recorder
	// Use direct API event creation instead of the broadcaster to prevent
	// event dropping when multiple nodes are remediated in quick succession.
	// Events are recorded in the pod's namespace (via POD_NAMESPACE env var)
	// so they appear alongside the CronJob, not in the default namespace.
	eventNamespace := os.Getenv("POD_NAMESPACE")
	if eventNamespace == "" {
		eventNamespace = "default"
	}
	recorder := &directEventRecorder{
		events: k8sClientset.CoreV1().Events(eventNamespace),
		namespace: eventNamespace,
	}

	// 10. Create uptime evaluator (uses K8s client for stats summary)
	uptimeEval := &k8sUptimeEvaluator{k8sClient: k8sClient}

	// 11. Create remediation manager
	deps := remediation.Dependencies{
		K8sClient:    k8sClient,
		DrainMgr:     drainMgr,
		StepFactory:  stepFactory,
		UptimeEval:   uptimeEval,
		GuardChecker: guardChecker,
		Discoverer:   discoverer,
		Config:       cfg,
		Recorder:     recorder,
	}

	mgr := remediation.NewManager(deps)

	// 12. Run one pass (CronJob triggers a new pod each run)
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	if err := mgr.Run(ctx); err != nil {
		log.Printf("remediation run failed: %v", err)
		os.Exit(1)
	}

	log.Println("────────────────────────────────────────────────────────────")
	log.Println("crusoe-node-remediation completed successfully")
}

// k8sUptimeEvaluator implements remediation.UptimeEvaluator using the K8s client.
type k8sUptimeEvaluator struct {
	k8sClient *k8s.Client
}

func (e *k8sUptimeEvaluator) GetNodeStartTime(ctx context.Context, nodeName string) (time.Time, error) {
	return e.k8sClient.GetNodeStartTime(ctx, nodeName)
}

// directEventRecorder creates K8s events directly via the API instead of
// using the broadcaster, which can drop events when multiple nodes are
// remediated in quick succession.
type directEventRecorder struct {
	events    typedcorev1.EventInterface
	namespace string
}

func (r *directEventRecorder) Event(obj runtime.Object, eventType, reason, message string) {
	r.createEvent(obj, eventType, reason, message)
}

func (r *directEventRecorder) Eventf(obj runtime.Object, eventType, reason, messageFmt string, args ...interface{}) {
	r.createEvent(obj, eventType, reason, fmt.Sprintf(messageFmt, args...))
}

func (r *directEventRecorder) AnnotatedEventf(obj runtime.Object, annotations map[string]string, eventType, reason, messageFmt string, args ...interface{}) {
	r.Eventf(obj, eventType, reason, messageFmt, args...)
}

func (r *directEventRecorder) createEvent(obj runtime.Object, eventType, reason, message string) {
	if r.events == nil {
		return
	}
	node, ok := obj.(*corev1.Node)
	if !ok {
		return
	}
	now := v1.Now()
	_, err := r.events.Create(context.Background(), &corev1.Event{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: "crusoe-node-remediation-",
			Namespace:    r.namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "Node",
			Name:       node.Name,
			UID:        node.UID,
			APIVersion:  "v1",
		},
		Reason:  reason,
		Message: message,
		Source: corev1.EventSource{
			Component: "crusoe-node-remediation",
		},
		FirstTimestamp: now,
		LastTimestamp:  now,
		Count:          1,
		Type:           eventType,
	}, v1.CreateOptions{})
	if err != nil {
		log.Printf("warning: failed to create event (reason=%s, node=%s): %v", reason, node.Name, err)
	}
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
