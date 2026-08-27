// Package guard contains no runtime code. It exists to hold the tests that
// enforce capsize's central promise structurally rather than by convention.
//
// The promise is: capsize cannot write to a cluster. Three things enforce it.
//
//  1. internal/k8s exposes only LIST and GET accessors, so no other package
//     can reach a mutating client method through the type system at all.
//  2. internal/k8s wraps the HTTP transport so that any request other than
//     GET, HEAD or OPTIONS fails at the socket.
//  3. This package's tests parse every .go file in the module and fail the
//     build if a write-shaped call, or an unauthorized client-go import,
//     appears anywhere in the tree.
//
// Point 3 is what keeps points 1 and 2 from rotting. If you are reading this
// because a test is failing: that is the test working.
package guard
