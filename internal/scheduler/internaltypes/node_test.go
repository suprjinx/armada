package internaltypes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/armadaproject/armada/internal/scheduler/schedulerobjects"
)

func TestNode(t *testing.T) {
	const id = "id"
	const nodeTypeId = uint64(123)
	const index = uint64(1)
	const executor = "executor"
	const name = "name"
	taints := []v1.Taint{
		{
			Key:   "foo",
			Value: "bar",
		},
	}
	labels := map[string]string{
		"key": "value",
	}
	totalResources := schedulerobjects.ResourceList{
		Resources: map[string]resource.Quantity{
			"cpu":    resource.MustParse("16"),
			"memory": resource.MustParse("32Gi"),
		},
	}
	allocatableByPriority := schedulerobjects.AllocatableByPriorityAndResourceType{
		1: {
			Resources: map[string]resource.Quantity{
				"cpu":    resource.MustParse("0"),
				"memory": resource.MustParse("0Gi"),
			},
		},
		2: {
			Resources: map[string]resource.Quantity{
				"cpu":    resource.MustParse("8"),
				"memory": resource.MustParse("16Gi"),
			},
		},
		3: {
			Resources: map[string]resource.Quantity{
				"cpu":    resource.MustParse("16"),
				"memory": resource.MustParse("32Gi"),
			},
		},
	}
	allocatedByQueue := map[string]schedulerobjects.ResourceList{
		"queue": {
			Resources: map[string]resource.Quantity{
				"cpu":    resource.MustParse("8"),
				"memory": resource.MustParse("16Gi"),
			},
		},
	}
	allocatedByJobId := map[string]schedulerobjects.ResourceList{
		"jobId": {
			Resources: map[string]resource.Quantity{
				"cpu":    resource.MustParse("8"),
				"memory": resource.MustParse("16Gi"),
			},
		},
	}
	evictedJobRunIds := map[string]bool{
		"jobId":        false,
		"evictedJobId": true,
	}
	keys := [][]byte{
		{
			0, 1, 255,
		},
	}

	node := CreateNode(
		id,
		nodeTypeId,
		index,
		executor,
		name,
		taints,
		labels,
		totalResources,
		allocatableByPriority,
		allocatedByQueue,
		allocatedByJobId,
		evictedJobRunIds,
		keys,
	)

	assert.Equal(t, id, node.GetId())
	assert.Equal(t, nodeTypeId, node.GetNodeTypeId())
	assert.Equal(t, index, node.GetIndex())
	assert.Equal(t, executor, node.GetExecutor())
	assert.Equal(t, name, node.GetName())
	assert.Equal(t, taints, node.Taints)
	assert.Equal(t, labels, node.Labels)
	assert.Equal(t, totalResources, node.TotalResources)
	assert.Equal(t, allocatableByPriority, node.AllocatableByPriority)
	assert.Equal(t, allocatedByQueue, node.AllocatedByQueue)
	assert.Equal(t, allocatedByJobId, node.AllocatedByJobId)
	assert.Equal(t, keys, node.Keys)

	nodeCopy := node.UnsafeCopy()
	node.Keys = nil // UnsafeCopy() sets Keys to nil
	assert.Equal(t, node, nodeCopy)
}
