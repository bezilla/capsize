// Command capsize scores Kubernetes workloads for cost waste and blast-radius
// exposure, and flags the places where those two goals contradict each other.
//
// capsize is read-only by construction. See internal/k8s for the enforcement
// and cmd/readonly_test.go for the test that keeps it that way.
package main

import "github.com/bezilla/capsize/cmd"

func main() {
	cmd.Execute()
}
