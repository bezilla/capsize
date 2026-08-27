// Package k8s is capsize's only door to the Kubernetes API, and it is a
// one-way door.
//
// The exported surface consists exclusively of accessors that GET or LIST.
// There is no Create, Update, Patch, Delete or Apply anywhere in the package,
// so no caller elsewhere in capsize can reach one - the write verbs are not
// merely discouraged, they are not in scope. Every request additionally
// travels through readOnlyTransport, which refuses any HTTP method other than
// GET, HEAD or OPTIONS.
package k8s

import (
	"context"
	"fmt"
	"net/http"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// MetricsGroupVersion is the API group capsize probes for usage data.
const MetricsGroupVersion = "metrics.k8s.io/v1beta1"

// Client is a read-only view of a cluster.
type Client struct {
	core    kubernetes.Interface
	metrics metricsclient.Interface

	// DefaultNamespace is the namespace bound to the selected kubeconfig
	// context, used when the caller passes neither -n nor -A.
	DefaultNamespace string

	// ContextName is the kubeconfig context actually in use, echoed in output
	// so nobody has to guess which cluster they just scanned.
	ContextName string
}

// Connect builds a read-only client from the caller's existing kubeconfig.
// It requests no credentials of its own: whatever kubectl can see, capsize
// can see, and nothing more.
func Connect(kubeconfig, kubecontext string) (*Client, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if kubecontext != "" {
		overrides.CurrentContext = kubecontext
	}

	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}

	// Belt and braces: every request in this process is wrapped, including
	// ones issued by client-go internals such as discovery.
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &readOnlyTransport{base: rt}
	})
	cfg.UserAgent = "capsize (read-only)"
	// A full scan lists a handful of collections; the client-go defaults of
	// 5 QPS make that needlessly slow on a large cluster.
	cfg.QPS = 50
	cfg.Burst = 100

	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building API client: %w", err)
	}
	metrics, err := metricsclient.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building metrics client: %w", err)
	}

	ns, _, err := loader.Namespace()
	if err != nil || ns == "" {
		ns = metav1.NamespaceDefault
	}

	name := kubecontext
	if name == "" {
		if raw, err := loader.RawConfig(); err == nil {
			name = raw.CurrentContext
		}
	}

	return &Client{core: core, metrics: metrics, DefaultNamespace: ns, ContextName: name}, nil
}

// newFromInterfaces is the seam used by tests to drive the accessors against
// a fake clientset. It is unexported on purpose.
func newFromInterfaces(core kubernetes.Interface, metrics metricsclient.Interface) *Client {
	return &Client{core: core, metrics: metrics, DefaultNamespace: metav1.NamespaceDefault}
}

// --- read accessors -------------------------------------------------------
//
// Every method below is a LIST or a GET. Adding anything else here is what
// internal/guard exists to prevent.

func (c *Client) Nodes(ctx context.Context) ([]corev1.Node, error) {
	l, err := c.core.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	return l.Items, nil
}

func (c *Client) Namespaces(ctx context.Context) ([]corev1.Namespace, error) {
	l, err := c.core.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing namespaces: %w", err)
	}
	return l.Items, nil
}

// Pods lists pods in ns, or cluster-wide when ns is "".
func (c *Client) Pods(ctx context.Context, ns string) ([]corev1.Pod, error) {
	l, err := c.core.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}
	return l.Items, nil
}

func (c *Client) Deployments(ctx context.Context, ns string) ([]appsv1.Deployment, error) {
	l, err := c.core.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing deployments: %w", err)
	}
	return l.Items, nil
}

func (c *Client) StatefulSets(ctx context.Context, ns string) ([]appsv1.StatefulSet, error) {
	l, err := c.core.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing statefulsets: %w", err)
	}
	return l.Items, nil
}

func (c *Client) DaemonSets(ctx context.Context, ns string) ([]appsv1.DaemonSet, error) {
	l, err := c.core.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing daemonsets: %w", err)
	}
	return l.Items, nil
}

func (c *Client) ReplicaSets(ctx context.Context, ns string) ([]appsv1.ReplicaSet, error) {
	l, err := c.core.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing replicasets: %w", err)
	}
	return l.Items, nil
}

func (c *Client) Jobs(ctx context.Context, ns string) ([]batchv1.Job, error) {
	l, err := c.core.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}
	return l.Items, nil
}

func (c *Client) CronJobs(ctx context.Context, ns string) ([]batchv1.CronJob, error) {
	l, err := c.core.BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing cronjobs: %w", err)
	}
	return l.Items, nil
}

func (c *Client) LimitRanges(ctx context.Context, ns string) ([]corev1.LimitRange, error) {
	l, err := c.core.CoreV1().LimitRanges(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing limitranges: %w", err)
	}
	return l.Items, nil
}

func (c *Client) ResourceQuotas(ctx context.Context, ns string) ([]corev1.ResourceQuota, error) {
	l, err := c.core.CoreV1().ResourceQuotas(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing resourcequotas: %w", err)
	}
	return l.Items, nil
}

// MetricsAvailable reports whether metrics-server (or an equivalent serving
// metrics.k8s.io) is reachable, and why not when it is not. capsize degrades
// to a static-only analysis rather than failing.
func (c *Client) MetricsAvailable(ctx context.Context) (bool, string) {
	_, err := c.core.Discovery().ServerResourcesForGroupVersion(MetricsGroupVersion)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, "metrics.k8s.io is not registered (metrics-server not installed)"
		}
		return false, fmt.Sprintf("metrics.k8s.io unreachable: %v", err)
	}
	return true, ""
}

// PodMetrics lists observed pod usage in ns, or cluster-wide when ns is "".
func (c *Client) PodMetrics(ctx context.Context, ns string) ([]metricsapi.PodMetrics, error) {
	l, err := c.metrics.MetricsV1beta1().PodMetricses(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pod metrics: %w", err)
	}
	return l.Items, nil
}

// compile-time assertion that the config we hand to client-go is a *rest.Config
var _ = (*rest.Config)(nil)
