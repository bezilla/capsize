# Security

capsize reads Kubernetes clusters. It is a CLI with no server, no agent and no
stored state, so its attack surface is small — but the one property it asks
you to trust is absolute, and that is where to look first.

## The claim

**capsize cannot write to your cluster.** Three mechanisms enforce it:

1. `internal/k8s` exposes no method that writes, so nothing else in the module
   can reach one through the type system.
2. The HTTP transport rejects every verb but GET, HEAD and OPTIONS.
3. `internal/guard` parses every `.go` file in the module at test time and
   fails the build on a write-shaped call or an unauthorized client-go import.

## Reporting a read-only-boundary bypass

**This is the report I most want to receive.** Anything that makes capsize
issue a mutating request — or could — is the highest-severity bug in this
repository, regardless of how contrived the path is:

- a code path that reaches a Create, Update, Patch, Delete, Apply, Eviction or
  subresource write on a real cluster;
- an HTTP request leaving the process with a method other than GET, HEAD or
  OPTIONS;
- a way to defeat `internal/guard`'s AST walk — an alias, a reflection-based
  call, a generated file, an import shape it does not recognize — such that a
  write-shaped call would ship green;
- anything that makes capsize write to the local filesystem outside the
  standard output it was asked for, or read a file it was not pointed at.

A proof of concept is welcome but not required. "This looks reachable and here
is why" is enough to act on.

Also in scope: credential handling (capsize should never log, cache or
transmit anything from your kubeconfig), and any way the tool could be made to
send cluster data anywhere — it makes no network call except to the API server
your kubeconfig names.

Out of scope: the risk scores being wrong. That is a correctness bug, and it
belongs in a normal issue.

## How to report

Use **[GitHub private vulnerability reporting](https://github.com/bezilla/capsize/security/advisories/new)**.
That keeps the report private until there is something to upgrade to.

If that is unavailable, open an issue saying only that you have a security
report and asking for a contact — no details in the issue.

Expect an acknowledgement within a week. This is a personal project maintained
by one person, so I will not promise a fix window I might miss; I will tell
you what I find and when I expect to ship, and I will credit you in the
release notes unless you would rather I did not.

## Supported versions

The latest release. capsize is pre-1.0 and there are no maintenance branches;
a fix ships in the next tag.

## Permissions

capsize requests no credentials of its own and needs no cluster-side install.
The complete set of permissions it uses, and what it does without each one, is
in [`docs/rbac.md`](docs/rbac.md).
