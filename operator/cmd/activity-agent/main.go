/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Command activity-agent watches the DevEnvironment it runs in and records when
// it was last used.
//
// It is a sidecar: it shares the environment pod's process and network
// namespaces, so it can see the user's processes and sockets, and it reports
// what it sees by writing one annotation on its own pod. Nothing else crosses
// the boundary — the controller cannot look inside a pod, and this is what
// tells it whether anyone is still there.
//
// The sampling interval and the shortest interval between two writes are not
// flags. They are the shape of the signal rather than a preference about this
// cluster: the controller reads the age of the annotation it finds, so a
// sampling cadence that varied per environment would make the same idle
// environment look different to the same controller.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/suanova/cubestack/internal/activity"
)

// defaultIgnoreComms are the daemons whose own housekeeping is not the user's
// work. Every one of them has no controlling terminal, so it would have to
// clear the CPU or IO threshold to count at all; the list is here for the case
// where one of them does — a background indexer, a language server polling a
// socket.
//
// They can be listed safely because none of them is where the user's work
// happens. A shell, an editor and a notebook kernel all run either on a
// terminal, which counts on any progress at all, or as a process that has to
// clear the threshold. Leaving a name out is the safer mistake: the environment
// then takes longer to look idle, where ignoring a name wrongly would stop an
// environment out from under someone who is still using it.
var defaultIgnoreComms = []string{"sshd", "containerd-shim", "runc", "code-server", "node"}

func main() {
	var podName, namespace, ports, ignoreComms string
	var cpuThreshold, ioThreshold uint64

	flag.StringVar(&podName, "pod", os.Getenv("POD_NAME"),
		"Name of the pod to record activity on. The downward API supplies it as POD_NAME.")
	flag.StringVar(&namespace, "namespace", os.Getenv("POD_NAMESPACE"),
		"Namespace of that pod. The downward API supplies it as POD_NAMESPACE.")
	flag.StringVar(&ports, "ports", "",
		"Comma-separated local ports whose connections count as the environment's. These are the "+
			"ports the environment's own services bind *inside* the pod, not the ports they are "+
			"published on. They are given rather than assumed so that this agent and the controller "+
			"that wires it up cannot disagree about them.")
	flag.Uint64Var(&cpuThreshold, "cpu-threshold", 20,
		"Clock ticks per sample that a process without a controlling terminal must burn to count "+
			"as activity. It separates a daemon's housekeeping from work. A process with a terminal "+
			"counts on any progress at all, and is not measured against this.")
	flag.Uint64Var(&ioThreshold, "io-threshold", 1<<20,
		"Bytes per sample that such a process must move to count as activity. It is what catches "+
			"work that is waiting on IO rather than burning CPU, such as a job writing checkpoints.")
	flag.StringVar(&ignoreComms, "ignore-comms", strings.Join(defaultIgnoreComms, ","),
		"Comma-separated process names that are never activity, whatever they do.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	log := ctrl.Log.WithName("activity-agent")
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// A configuration this cannot work from stops here rather than at the first
	// tick, and that is a deliberate exception to how it treats every other
	// failure. This agent is not allowed to fail *about the environment*: a
	// sampling or API error is logged and waited out, because a sidecar that is
	// not Running marks the whole environment Failed and stops the user's work.
	// Being handed nothing to watch is not a judgement about the environment, it
	// is the controller having wired the sidecar wrong — and an agent that
	// carried on would record nothing, leaving the environment to be stopped as
	// idle while someone was using it.
	watched, err := parsePorts(ports)
	if err != nil {
		log.Error(err, "Invalid configuration")
		os.Exit(1)
	}
	if podName == "" || namespace == "" {
		log.Error(errors.New("--pod and --namespace are both required"), "Invalid configuration")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	config, err := ctrl.GetConfig()
	if err != nil {
		log.Error(err, "No cluster to reach")
		os.Exit(1)
	}
	// The pod is reached through the service account the controller scopes to
	// this one environment, so the only thing this credential can do is read and
	// annotate the pod it is already running in.
	api, err := client.New(config, client.Options{Scheme: scheme()})
	if err != nil {
		log.Error(err, "Failed to build the API client")
		os.Exit(1)
	}

	detector := activity.NewDetector(
		activity.Config{
			CPUThreshold: cpuThreshold,
			IOThreshold:  ioThreshold,
			IgnoreComms:  commSet(ignoreComms),
		},
		activity.NewConnSampler(watched),
		activity.NewProcSampler(),
		os.Getpid(),
	)
	store := activity.NewPodAnnotationStore(api, types.NamespacedName{Namespace: namespace, Name: podName})

	log.Info("Starting",
		"pod", podName, "namespace", namespace, "ports", watched,
		"cpuThreshold", cpuThreshold, "ioThreshold", ioThreshold,
		"sampleInterval", activity.SampleInterval.String(),
		"minWriteInterval", activity.MinWriteInterval.String())

	ticker := time.NewTicker(activity.SampleInterval)
	defer ticker.Stop()

	ticks := make(chan time.Time)
	go func() {
		defer close(ticks)
		for {
			select {
			case <-ctx.Done():
				return
			case tick := <-ticker.C:
				select {
				case ticks <- tick:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	// Run returns when the signal context is cancelled or the channel closes,
	// and has nothing to report either way. A non-zero exit would be a restart,
	// and a restarting sidecar is a failed environment.
	activity.NewRecorder(detector, store, log).Run(ctx, ticks)
}

// scheme is the little of the Kubernetes API this needs: the core objects,
// because the pod is one.
func scheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	return s
}

// parsePorts reads the watched ports. A field that is not a port is refused
// rather than dropped, so that a typo in the controller's wiring is a sidecar
// that says so rather than one that quietly watches nothing.
func parsePorts(ports string) ([]int32, error) {
	var watched []int32
	for field := range strings.SplitSeq(ports, ",") {
		if field = strings.TrimSpace(field); field == "" {
			continue
		}
		port, err := strconv.ParseInt(field, 10, 32)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("--ports: %q is not a port between 1 and 65535", field)
		}
		watched = append(watched, int32(port))
	}
	return watched, nil
}

// commSet builds the ignore list from the comma-separated flag.
func commSet(comms string) map[string]struct{} {
	set := make(map[string]struct{})
	for comm := range strings.SplitSeq(comms, ",") {
		if comm = strings.TrimSpace(comm); comm != "" {
			set[comm] = struct{}{}
		}
	}
	return set
}
