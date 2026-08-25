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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DevEnvironmentSpec defines the desired state of DevEnvironment
type DevEnvironmentSpec struct {
	// Type is the container type (single choice): jupyter / ssh / vscode (P3).
	// It decides the container main entry and the image (see section 6.1).
	// +kubebuilder:validation:Enum=jupyter;ssh;vscode
	// +kubebuilder:default=jupyter
	// +optional
	Type string `json:"type,omitempty"`

	// Image is the development image, pulled from Harbor. Supports the
	// base-cuda (NVIDIA) / base-maca (Moore Threads) series.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Running is the desired running state: true=Running (replicas 1),
	// false=Stopped (replicas 0).
	// +kubebuilder:default=true
	// +optional
	Running bool `json:"running,omitempty"`

	// Resources is the compute / resource configuration.
	Resources ResourcesSpec `json:"resources"`

	// Storage is the workspace storage (a workspace volume created with the
	// environment and mounted to $HOME by default).
	// +optional
	Storage *StorageSpec `json:"storage,omitempty"`

	// Volumes are extra data volume mounts (referencing existing PVCs).
	// +optional
	Volumes []VolumeMount `json:"volumes,omitempty"`

	// SSH configures SSH access.
	// +optional
	SSH *SSHSpec `json:"ssh,omitempty"`

	// Network configures the network.
	// +optional
	Network *NetworkSpec `json:"network,omitempty"`

	// Runtime customizes the container runtime.
	// +optional
	Runtime *RuntimeSpec `json:"runtime,omitempty"`

	// Lifecycle configures the lifecycle.
	// +optional
	Lifecycle *LifecycleSpec `json:"lifecycle,omitempty"`

	// Ports are extra application ports (P2, DEV-16; not opened in P1).
	// +optional
	Ports []PortSpec `json:"ports,omitempty"`

	// AssetRefs bind assets (P2: Model / Dataset CRD references, auto-mounted
	// and updated with the version).
	// +optional
	AssetRefs []AssetRef `json:"assetRefs,omitempty"`

	// TemplateRef is the source template reference (P2: provenance / label
	// inheritance; resolved values are fixed into spec, see section 13.1).
	// +optional
	TemplateRef *TemplateRef `json:"templateRef,omitempty"`

	// Scheduling configures scheduling (P3: Kueue priority / queue).
	// +optional
	Scheduling *SchedulingSpec `json:"scheduling,omitempty"`
}

// ResourcesSpec is the compute / resource configuration.
type ResourcesSpec struct {
	// ComputeProfile is the compute profile name, referencing a ComputeProfile
	// CRD; the materialization source of cpu/memory (copied on first
	// reconcile), keeping provenance.
	// +kubebuilder:validation:MinLength=1
	ComputeProfile string `json:"computeProfile"`

	// GPUType is the GPU vendor: nvidia / metax. It decides the GPU extended
	// resource (nvidia.com/gpu / metax-tech.com/gpu) and image brand matching.
	// +kubebuilder:validation:Enum=nvidia;metax
	// +kubebuilder:validation:MinLength=1
	GPUType string `json:"gpuType"`

	// GPUCount is the number of GPU cards.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	GPUCount int32 `json:"gpuCount,omitempty"`

	// CPU is the number of CPU cores (materialized from ComputeProfile on first
	// reconcile; user can override afterwards).
	// +optional
	CPU string `json:"cpu,omitempty"`

	// Memory is the memory size (materialized from ComputeProfile on first
	// reconcile; user can override afterwards).
	// +optional
	Memory string `json:"memory,omitempty"`
}

// StorageSpec is the workspace storage configuration.
type StorageSpec struct {
	// Size is the workspace PVC capacity.
	// +kubebuilder:default="100Gi"
	// +optional
	Size string `json:"size,omitempty"`

	// StorageClassName is the workspace storage class; a CephFS / NFS
	// StorageClass yields an RWX workspace.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// PVCRetention is the workspace PVC retention policy on environment delete
	// (D14): retain=keep (default, prevents accidental deletion) / delete=remove
	// with the environment.
	// +kubebuilder:validation:Enum=retain;delete
	// +kubebuilder:default=retain
	// +optional
	PVCRetention PVCRetentionPolicy `json:"pvcRetention,omitempty"`

	// MountPath is the mount path (optional); by default the controller
	// materializes it from whether the environment runs as root:
	// non-root (runAsNonRoot=true / runAsUser!=0) -> /home; root (runAsUser=0)
	// -> /root.
	// +optional
	MountPath string `json:"mountPath,omitempty"`
}

// PVCRetentionPolicy is the workspace PVC deletion policy.
type PVCRetentionPolicy string

const (
	PVCRetentionRetain PVCRetentionPolicy = "retain"
	PVCRetentionDelete PVCRetentionPolicy = "delete"
)

// VolumeMount is an extra data volume mount (referencing an existing PVC).
type VolumeMount struct {
	// Name is the volume identifier.
	Name string `json:"name"`

	// PVCName is the name of the referenced existing PVC (P1, shared data volume).
	PVCName string `json:"pvcName"`

	// MountPath is the mount path (e.g. /data, /models).
	MountPath string `json:"mountPath"`

	// SubPath is the sub path (optional).
	// +optional
	SubPath string `json:"subPath,omitempty"`

	// ReadOnly indicates whether the volume is read-only (default false).
	// +optional
	ReadOnly bool `json:"readOnly,omitempty"`
}

// SSHSpec configures SSH access.
type SSHSpec struct {
	// Enabled enables SSH access for jupyter / vscode types (runs sshd inside
	// the container); the ssh type always has it enabled.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// KeysSecret is the SSH public key Secret reference (D13):
	// Secret.data[key] stores multi-line public keys (authorized_keys content),
	// read by the controller and injected into the container; the plaintext is
	// not stored in spec.
	// +optional
	KeysSecret *corev1.SecretKeySelector `json:"keysSecret,omitempty"`
}

// NetworkSpec configures the network.
type NetworkSpec struct {
	// RDMAEnabled enables the RDMA network (Multus).
	// +kubebuilder:default=false
	// +optional
	RDMAEnabled bool `json:"rdmaEnabled,omitempty"`

	// RDMAType is the RDMA network type: infiniband (requires IB switches + a
	// subnet manager) / roce (RoCEv2, reuses lossless ethernet); effective when
	// rdmaEnabled=true.
	// +kubebuilder:validation:Enum=infiniband;roce
	// +kubebuilder:default=roce
	// +optional
	RDMAType RDMAType `json:"rdmaType,omitempty"`
}

// RDMAType is the RDMA network type.
type RDMAType string

const (
	RDMATypeInfiniBand RDMAType = "infiniband"
	RDMATypeRoCE       RDMAType = "roce"
)

// RuntimeSpec customizes the container runtime.
type RuntimeSpec struct {
	// Command overrides the startup command.
	// +optional
	Command []string `json:"command,omitempty"`

	// Args overrides the startup arguments.
	// +optional
	Args []string `json:"args,omitempty"`

	// Env is the environment variables (name/value or valueFrom: secretKeyRef).
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// SecurityContext controls the container user (DEV-31): non-root by default
	// (runAsNonRoot=true, runAsUser=1000); running as root requires explicitly
	// setting runAsUser=0 (and ensuring runAsNonRoot is not true).
	// +optional
	SecurityContext *corev1.SecurityContext `json:"securityContext,omitempty"`
}

// LifecycleSpec configures the lifecycle.
type LifecycleSpec struct {
	// IdleTimeout is the idle auto-shutdown timeout in seconds; 0 disables it.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	IdleTimeout int32 `json:"idleTimeout,omitempty"`
}

// PortSpec is an extra application port (P2, DEV-16).
type PortSpec struct {
	// Name is the port identifier (unique, used for sub path / status display).
	Name string `json:"name"`

	// Type is the exposure form: http (web over sub path) / tcp (port range +
	// TCPRoute) / udp (needs UDPRoute experimental feature, P2 pending).
	// +kubebuilder:validation:Enum=http;tcp;udp
	// +kubebuilder:default=http
	Type PortType `json:"type"`

	// ContainerPort is the in-container application port.
	ContainerPort int32 `json:"containerPort"`
}

// PortType is the extra application port exposure form.
type PortType string

const (
	PortTypeHTTP PortType = "http"
	PortTypeTCP  PortType = "tcp"
	PortTypeUDP  PortType = "udp"
)

// AssetRef is an asset binding reference.
type AssetRef struct {
	// Kind is the asset kind.
	// +kubebuilder:validation:Enum=Model;Dataset
	Kind string `json:"kind"`

	// Name is the asset CRD name.
	Name string `json:"name"`

	// MountPath is the mount path (e.g. /models).
	MountPath string `json:"mountPath"`
}

// TemplateRef is the source template reference.
type TemplateRef struct {
	// Name is the template CRD name.
	Name string `json:"name"`
}

// SchedulingSpec configures scheduling (P3).
type SchedulingSpec struct {
	// Priority is the priority: low / normal / high / urgent (with Kueue
	// preemption).
	// +kubebuilder:validation:Enum=low;normal;high;urgent
	// +kubebuilder:default=normal
	// +optional
	Priority string `json:"priority,omitempty"`

	// Queue is the Kueue LocalQueue name.
	// +kubebuilder:default=default
	// +optional
	Queue string `json:"queue,omitempty"`
}

// DevEnvironmentStatus defines the observed state of DevEnvironment.
type DevEnvironmentStatus struct {
	// Phase is the running phase.
	// +kubebuilder:validation:Enum=Pending;Running;Stopped;Failed;Terminating
	// +optional
	Phase string `json:"phase,omitempty"`

	// Reason is the current status reason, recording the error on failure.
	// +optional
	Reason string `json:"reason,omitempty"`

	// URL is the Jupyter / Web access address (path-routed via the gateway IP,
	// e.g. http://<gw-ip>:80/dev/<ns>/<env>/).
	// +optional
	URL string `json:"url,omitempty"`

	// SSHEndpoint is the SSH connection address (gateway TCP port range),
	// format ssh://user@<gw-ip>:<env-ssh-port>.
	// +optional
	SSHEndpoint string `json:"sshEndpoint,omitempty"`

	// PodName is the associated Pod name (empty when Stopped).
	// +optional
	PodName string `json:"podName,omitempty"`

	// NodeName is the scheduled node name.
	// +optional
	NodeName string `json:"nodeName,omitempty"`

	// StartTime is the start time.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// LastActivityTime is the last activity time, used for idle timeout
	// determination (DEV-27 / DEV-02 display).
	// +optional
	LastActivityTime *metav1.Time `json:"lastActivityTime,omitempty"`

	// ComputeProfileGeneration is the generation of the materialized source
	// ComputeProfile (audit provenance / drift detection; written on first
	// materialization).
	// +optional
	ComputeProfileGeneration int64 `json:"computeProfileGeneration,omitempty"`

	// TemplateGeneration is the generation of the source template (only when
	// spec.templateRef exists; audit provenance).
	// +optional
	TemplateGeneration int64 `json:"templateGeneration,omitempty"`

	// AppEndpoints are the extra application port exposure addresses (P2,
	// DEV-16): http type -> url (sub path); tcp / udp type -> endpoint
	// (host:port).
	// +optional
	AppEndpoints []AppEndpoint `json:"appEndpoints,omitempty"`

	// Conditions: PodScheduled / StorageReady / BrandMatchValid /
	// ComputeProfileReady / TemplateRefReady / Ready (type constants below).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// AppEndpoint is an extra application port exposure address (P2, DEV-16,
// corresponding to spec.ports).
type AppEndpoint struct {
	// Name corresponds to spec.ports[].name.
	Name string `json:"name"`

	// URL is the sub path access address for the http type.
	// +optional
	URL string `json:"url,omitempty"`

	// Endpoint is the host:port access address for the tcp / udp type.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced

// DevEnvironment is the Schema for the devenvironments API
type DevEnvironment struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DevEnvironment
	// +required
	Spec DevEnvironmentSpec `json:"spec"`

	// status defines the observed state of DevEnvironment
	// +optional
	Status DevEnvironmentStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DevEnvironmentList contains a list of DevEnvironment
type DevEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DevEnvironment `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DevEnvironment{}, &DevEnvironmentList{})
		return nil
	})
}
