package cmd

import (
	"strings"
	"testing"

	"github.com/bezilla/capsize/internal/detect"
	"github.com/bezilla/capsize/internal/model"
	"github.com/bezilla/capsize/internal/output"
	"github.com/bezilla/capsize/internal/units"
)

func defaults() *Options {
	return &Options{
		RequestFloorStr: "10Mi", MinRequestStr: "32Mi",
		Divergence: 2, IdleRatio: 50, Headroom: 1.25, FailOn: "none",
	}
}

func TestValidateRejectsBadFlagsBeforeTouchingTheCluster(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Options)
		want string
	}{
		{"unparseable floor", func(o *Options) { o.RequestFloorStr = "banana" }, "--request-floor"},
		{"zero floor", func(o *Options) { o.RequestFloorStr = "0" }, "greater than zero"},
		{"unparseable min-request", func(o *Options) { o.MinRequestStr = "lots" }, "--min-request"},
		{"zero min-request", func(o *Options) { o.MinRequestStr = "0" }, "greater than zero"},
		{"divergence of one", func(o *Options) { o.Divergence = 1 }, "--divergence"},
		{"idle-ratio below divergence", func(o *Options) { o.IdleRatio = 1.5 }, "--idle-ratio"},
		{"headroom below one", func(o *Options) { o.Headroom = 0.5 }, "--headroom"},
		{"unknown severity", func(o *Options) { o.FailOn = "loud" }, "--fail-on"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := defaults()
			tc.mut(o)
			if _, _, err := validate(o); err == nil {
				t.Fatal("expected an error")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

func TestValidateAcceptsTheDefaults(t *testing.T) {
	tune, failOn, err := validate(defaults())
	if err != nil {
		t.Fatal(err)
	}
	if tune.requestFloor != 10*units.Mi {
		t.Errorf("requestFloor = %d, want %d", tune.requestFloor, 10*units.Mi)
	}
	if tune.minRequest != 32*units.Mi {
		t.Errorf("minRequest = %d, want %d", tune.minRequest, 32*units.Mi)
	}
	if failOn != "" {
		t.Errorf(`--fail-on none must disable the gate, got %q`, failOn)
	}
}

func TestNamespaceAndAllNamespacesAreMutuallyExclusive(t *testing.T) {
	o := defaults()
	o.AllNS, o.Namespace = true, "prod"
	if _, err := resolveNamespace(o, nil); err == nil {
		t.Fatal("-A with -n is ambiguous and must be rejected")
	}
}

func gateReport(risk float64, sev detect.Severity) *output.Report {
	ref := model.Ref{Kind: model.KindDeployment, Namespace: "prod", Name: "api"}
	r := &output.Report{MaxRisk: risk, MaxRiskRef: &ref}
	if sev != "" {
		r.Findings = []detect.Finding{{Rule: "CAP102", Severity: sev}}
	}
	return r
}

func TestGateIsOffByDefault(t *testing.T) {
	o := defaults()
	_, failOn, _ := validate(o)
	if err := gate(o, failOn, gateReport(9999, detect.SeverityCritical)); err != nil {
		t.Fatalf("with no threshold and --fail-on none, capsize must exit 0: %v", err)
	}
}

func TestRiskThresholdTrips(t *testing.T) {
	o := defaults()
	o.RiskThreshold = 50

	if err := gate(o, "", gateReport(49, "")); err != nil {
		t.Fatalf("below the threshold must pass: %v", err)
	}
	err := gate(o, "", gateReport(51, ""))
	if err == nil {
		t.Fatal("above the threshold must fail")
	}
	if _, ok := err.(thresholdError); !ok {
		t.Fatalf("want thresholdError so Execute exits 2, got %T", err)
	}
	for _, want := range []string{"51", "50", "prod/api"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("a failed pipeline must say why; %q is missing %q", err, want)
		}
	}
}

func TestFailOnSeverityTrips(t *testing.T) {
	o := defaults()

	if err := gate(o, detect.SeverityCritical, gateReport(0, detect.SeverityWarn)); err != nil {
		t.Fatalf("warn must not trip --fail-on critical: %v", err)
	}
	if err := gate(o, detect.SeverityWarn, gateReport(0, detect.SeverityWarn)); err == nil {
		t.Fatal("warn must trip --fail-on warn")
	}
	if err := gate(o, detect.SeverityWarn, gateReport(0, detect.SeverityCritical)); err == nil {
		t.Fatal("critical must trip --fail-on warn")
	}
}

func TestBothGatesReportTogether(t *testing.T) {
	o := defaults()
	o.RiskThreshold = 10
	err := gate(o, detect.SeverityWarn, gateReport(99, detect.SeverityCritical))
	if err == nil {
		t.Fatal("expected a failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "risk-threshold") || !strings.Contains(msg, "fail-on") {
		t.Errorf("both reasons should be reported, got %q", msg)
	}
}

func TestExplicitSystemNamespaceIsHonoured(t *testing.T) {
	// Excluded "unless asked". Asking is -n kube-system.
	for _, ns := range []string{"kube-system", "kube-public", "local-path-storage"} {
		if !model.SystemNamespace(ns) {
			t.Fatalf("%s should classify as a system namespace", ns)
		}
	}
	if model.SystemNamespace("") {
		t.Error("cluster-wide scope must not be treated as a system namespace, or -A would never filter")
	}
	if model.SystemNamespace("prod") {
		t.Error("prod is the user's own")
	}
}
