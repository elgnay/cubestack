# DevEnvironment lifecycle: starting, stopping, and the idle auto-stop

## 1. Two fields, one question

`spec.running` is what the **user** asked for. `status.phase` is what the platform **observes**.
They usually agree, and there is exactly one situation in which they deliberately do not — an
environment stopped by its idle timeout.

The platform never writes `spec.running`. Not on a stop, and not on an auto-stop. A field the user
owns stays as the user wrote it, so a `DevEnvironment` managed declaratively (the object in git, or
under a GitOps controller) does not fight the platform over a field both would claim.

## 2. The two ways an environment reaches `Stopped`

| Way | `spec.running` | `status.phase` | `status.phase.reason` |
|---|---|---|---|
| The user stops it | set to `false` by the user | `Stopped` | `Stopped` |
| The idle timeout stops it | **untouched** — still `true` | `Stopped` | `IdleTimeout` |

An environment whose `spec.running` is `true` and whose phase is `Stopped` therefore reads, correctly,
as one the user considers running that the platform has paused for them. The **reason** is what tells
the two stops apart — a user's stop from the platform's — which is why `kubectl get devenv` prints it
as a column of its own:

```
NAMESPACE   NAME                PHASE     REASON             LAST ACTIVITY   AGE
default     demo-rdma           Failed    ImagePullBackOff                   67m
default     jupyter             Running   Running                            18h
default     jupyter-cuda-test   Stopped   IdleTimeout        20h             21h
default     jupyter-maca        Pending   Pending                            22h
```

`LAST ACTIVITY` is the agent's own report (`status.lastActivityTime`) and is blank until the
environment has been active at least once, so the `Running` row above has nothing to show there while
the idle-stopped one has the mark that explains it: last used 20h ago, stopped by the 30m timeout on
`spec.lifecycle.idleTimeout` shortly after.

## 3. What an idle stop does

`spec.lifecycle.idleTimeout` is the number of seconds of inactivity after which the environment is
stopped (0, the default, disables it). Activity is reported by the environment's own
`activity-agent` sidecar, which writes the pod annotation `ai.cubestack.io/last-activity`; the
controller reads that back into `status.lastActivityTime` and stops the environment when it has
stood still for longer than the timeout.

An auto-stop:

- **sets `ai.cubestack.io/auto-stopped: "true"` on the DevEnvironment** — this annotation, not
  `spec.running`, is the stopped state;
- scales the workload to zero replicas, which is how a *derived* fact follows from the mark;
- reports `phase: Stopped` with `reason: IdleTimeout`;
- **keeps the workspace claim** — the stop releases compute, not data;
- withholds the access endpoints, since nothing is listening behind them;
- records a `Normal` Event under reason `IdleTimeout`, so the stop is visible in the audit trail
  rather than only in the current state;
- leaves `spec.running` exactly as it was.

The controller also clears the mark on its own in two cases, both of which mean the mark no longer
describes the environment: **the user stops the environment**, or **the idle timeout is turned
off**. Neither is a start.

## 4. Starting an auto-stopped environment

**The mark is the whole of the stopped state, so clearing it is the start.** With `spec.running`
already `true` there is nothing else for the controller to observe:

```
# does NOT work — the value is already true, so this patch changes nothing at
# all, and in particular it leaves ai.cubestack.io/auto-stopped standing
kubectl patch devenv <name> --type merge -p '{"spec":{"running":true}}'
```

That patch is a no-op *as a write*: no object change, no `resourceVersion` bump, no watch event.
But the absent wake-up is not why the environment stays down, and saying so would mislead a client
that goes looking for a retry. The controller reconciles an environment on changes to its
StatefulSet, its pod and its Service as well, and any such pass that sees the object unchanged
finds the mark still standing: it is kept, and zero replicas are derived from it again. **The mark
is the obstacle, not the sleeping controller.**

Two ways that do work:

```
# one write: clear the mark
kubectl annotate devenv <name> ai.cubestack.io/auto-stopped-
```

```
# or two: the stop clears the mark, which makes the start a real change
kubectl patch devenv <name> --type merge -p '{"spec":{"running":false}}'
kubectl patch devenv <name> --type merge -p '{"spec":{"running":true}}'
```

The web console's Start is to do the first of these on the user's behalf. It does not yet: it sends
only `spec.running`, so on an auto-stopped environment it silently does nothing (issue #248).

**Never clear `ai.cubestack.io/auto-stopped-at`.** That key is the controller's own record of *when*
it stopped the environment, and it is a floor under the idle clock. The pod from the session that
just ended is usually still standing for a moment after the stop, still carrying the stale activity
mark that made it look idle; without the floor the very next pass would judge it idle again and
re-mark the environment the user has just started. It is the controller's to write and to read, and
no client should touch it.

## 5. The alternative not taken

The platform could have written `spec.running: false` on an auto-stop, which would give every client
a spec-only start for free — `kubectl patch … running:true` would "just work". It is not taken
because it hands the platform a field the user's tooling owns, and the cost is paid back on every
GitOps-managed environment.

The cost of the decision taken is this document: the annotation is public API, and a client has to
know it. It is a platform-owned field, so the ownership split stays clean — but the mark is only
legible if it is written down, which is what the sections above are for.

## 6. Where this lives

- `operator/api/v1alpha1/devenvironment_types.go` — the contract, on `spec.lifecycle.idleTimeout`
  and on `DevEnvironment` itself, plus the printer columns the table in §2 comes from.
- `operator/internal/controller/idle.go` — the judgement (`decideIdle`), the mark
  (`markAutoStopped`, `clearAutoStopped`, `clearSupersededAutoStop`) and what the clock is measured
  against.
- `operator/internal/activity/` and `cmd/activity-agent` — the sidecar that reports activity.
