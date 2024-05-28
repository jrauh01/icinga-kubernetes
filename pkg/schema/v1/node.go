package v1

import (
	"github.com/icinga/icinga-go-library/types"
	"github.com/icinga/icinga-kubernetes/pkg/database"
	"github.com/pkg/errors"
	kcorev1 "k8s.io/api/core/v1"
	kmetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	knet "k8s.io/utils/net"
	"net"
	"strings"
)

type Node struct {
	Meta
	PodCIDR           string
	NumIps            int64
	Unschedulable     types.Bool
	Ready             types.Bool
	CpuCapacity       int64
	CpuAllocatable    int64
	MemoryCapacity    int64
	MemoryAllocatable int64
	PodCapacity       int64
	Conditions        []NodeCondition `db:"-"`
	Volumes           []NodeVolume    `db:"-"`
	Labels            []Label         `db:"-"`
	NodeLabels        []NodeLabel     `db:"-"`
}

type NodeCondition struct {
	NodeUuid       types.UUID
	Type           string
	Status         string
	LastHeartbeat  types.UnixMilli
	LastTransition types.UnixMilli
	Reason         string
	Message        string
}

type NodeVolume struct {
	NodeUuid   types.UUID
	Name       kcorev1.UniqueVolumeName
	DevicePath string
	Mounted    types.Bool
}

type NodeLabel struct {
	NodeUuid  types.UUID
	LabelUuid types.UUID
}

func NewNode() Resource {
	return &Node{}
}

func (n *Node) Obtain(k8s kmetav1.Object) {
	n.ObtainMeta(k8s)

	node := k8s.(*kcorev1.Node)

	n.PodCIDR = node.Spec.PodCIDR
	if n.PodCIDR != "" {
		_, cidr, err := net.ParseCIDR(n.PodCIDR)
		if err != nil {
			panic(errors.Wrapf(err, "failed to parse CIDR %s", n.PodCIDR))
		}
		n.NumIps = knet.RangeSize(cidr) - 2
	}
	n.Unschedulable = types.Bool{
		Bool:  node.Spec.Unschedulable,
		Valid: true,
	}
	n.Ready = types.Bool{
		Bool:  getNodeConditionStatus(node, kcorev1.NodeReady),
		Valid: true,
	}
	n.CpuCapacity = node.Status.Capacity.Cpu().MilliValue()
	n.CpuAllocatable = node.Status.Allocatable.Cpu().MilliValue()
	n.MemoryCapacity = node.Status.Capacity.Memory().MilliValue()
	n.MemoryAllocatable = node.Status.Allocatable.Memory().MilliValue()
	n.PodCapacity = node.Status.Allocatable.Pods().Value()

	for _, condition := range node.Status.Conditions {
		n.Conditions = append(n.Conditions, NodeCondition{
			NodeUuid:       n.Uuid,
			Type:           string(condition.Type),
			Status:         string(condition.Status),
			LastHeartbeat:  types.UnixMilli(condition.LastHeartbeatTime.Time),
			LastTransition: types.UnixMilli(condition.LastTransitionTime.Time),
			Reason:         condition.Reason,
			Message:        condition.Message,
		})
	}

	volumesMounted := make(map[kcorev1.UniqueVolumeName]interface{}, len(node.Status.VolumesInUse))
	for _, name := range node.Status.VolumesInUse {
		volumesMounted[name] = struct{}{}
	}

	for _, volume := range node.Status.VolumesAttached {
		_, mounted := volumesMounted[volume.Name]
		n.Volumes = append(n.Volumes, NodeVolume{
			NodeUuid:   n.Uuid,
			Name:       volume.Name,
			DevicePath: volume.DevicePath,
			Mounted: types.Bool{
				Bool:  mounted,
				Valid: true,
			},
		})
	}

	for labelName, labelValue := range node.Labels {
		labelUuid := NewUUID(n.Uuid, strings.ToLower(labelName+":"+labelValue))
		n.Labels = append(n.Labels, Label{
			Uuid:  labelUuid,
			Name:  labelName,
			Value: labelValue,
		})
		n.NodeLabels = append(n.NodeLabels, NodeLabel{
			NodeUuid:  n.Uuid,
			LabelUuid: labelUuid,
		})
	}
}

func (n *Node) Relations() []database.Relation {
	fk := database.WithForeignKey("node_uuid")

	return []database.Relation{
		database.HasMany(n.Conditions, fk),
		database.HasMany(n.Volumes, fk),
		database.HasMany(n.NodeLabels, fk),
		database.HasMany(n.Labels, database.WithoutCascadeDelete()),
	}
}

func getNodeConditionStatus(node *kcorev1.Node, conditionType kcorev1.NodeConditionType) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == conditionType {
			return true
		}
	}

	return false
}
