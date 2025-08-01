package nodedb

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	armadamaps "github.com/armadaproject/armada/internal/common/maps"
	"github.com/armadaproject/armada/internal/common/pointer"
	"github.com/armadaproject/armada/internal/common/util"
	schedulerconfig "github.com/armadaproject/armada/internal/scheduler/configuration"
	"github.com/armadaproject/armada/internal/scheduler/internaltypes"
	"github.com/armadaproject/armada/internal/scheduler/jobdb"
	"github.com/armadaproject/armada/internal/scheduler/schedulerobjects"
	"github.com/armadaproject/armada/internal/scheduler/scheduling/context"
	"github.com/armadaproject/armada/internal/scheduler/testfixtures"
)

func TestNodeDbSchema(t *testing.T) {
	schema, _, _ := nodeDbSchema(testfixtures.TestPriorities, testfixtures.TestResourceNames)
	assert.NoError(t, schema.Validate())
}

// Test the accounting of total resources across all nodes.
func TestTotalResources(t *testing.T) {
	nodeDb, err := newNodeDbWithNodes([]*internaltypes.Node{})
	require.NoError(t, err)

	assert.False(t, nodeDb.TotalKubernetesResources().IsEmpty())
	assert.True(t, nodeDb.TotalKubernetesResources().AllZero())

	expected := testfixtures.TestNodeFactory.ResourceListFactory().MakeAllZero()
	// Upserting nodes for the first time should increase the resource count.
	nodes := testfixtures.N32CpuNodes(2, testfixtures.TestPriorities)
	for _, node := range nodes {
		expected = expected.Add(node.GetTotalResources())
	}
	txn := nodeDb.Txn(true)
	for _, node := range nodes {
		err = nodeDb.CreateAndInsertWithJobDbJobsWithTxn(txn, nil, node)
		require.NoError(t, err)
	}
	txn.Commit()

	assert.True(t, expected.Equal(nodeDb.TotalKubernetesResources()))

	// Upserting new nodes should increase the resource count.
	nodes = testfixtures.N8GpuNodes(3, testfixtures.TestPriorities)
	for _, node := range nodes {
		expected = expected.Add(node.GetTotalResources())
	}
	txn = nodeDb.Txn(true)
	for _, node := range nodes {
		err = nodeDb.CreateAndInsertWithJobDbJobsWithTxn(txn, nil, node)
		require.NoError(t, err)
	}
	txn.Commit()

	assert.True(t, expected.Equal(nodeDb.TotalKubernetesResources()))
}

func TestSelectNodeForPod_NodeIdLabel_Success(t *testing.T) {
	nodes := testfixtures.N32CpuNodes(2, testfixtures.TestPriorities)
	nodeId := nodes[1].GetId()
	require.NotEmpty(t, nodeId)
	db, err := newNodeDbWithNodes(nodes)
	require.NoError(t, err)
	jobs := testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)
	jctxs := context.JobSchedulingContextsFromJobs(jobs)
	for _, jctx := range jctxs {
		txn := db.Txn(false)
		jctx.SetAssignedNode(testfixtures.TestSimpleNode(nodeId))
		node, err := db.SelectNodeForJobWithTxn(txn, jctx)
		txn.Abort()
		require.NoError(t, err)
		pctx := jctx.PodSchedulingContext
		if assert.NotNil(t, node) {
			assert.Equal(t, nodeId, node.GetId())
		}
		if assert.NotNil(t, pctx) {
			assert.Equal(t, nodeId, pctx.NodeId)
			assert.Empty(t, pctx.NumExcludedNodesByReason, "got %v", pctx.NumExcludedNodesByReason)
		}
	}
}

func TestSelectNodeForPod_NodeIdLabel_Failure(t *testing.T) {
	nodes := testfixtures.N32CpuNodes(1, testfixtures.TestPriorities)
	nodeId := nodes[0].GetId()
	require.NotEmpty(t, nodeId)
	db, err := newNodeDbWithNodes(nodes)
	require.NoError(t, err)
	jobs := testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)
	jctxs := context.JobSchedulingContextsFromJobs(jobs)
	for _, jctx := range jctxs {
		txn := db.Txn(false)
		jctx.SetAssignedNode(testfixtures.TestSimpleNode("non-existent node"))
		node, err := db.SelectNodeForJobWithTxn(txn, jctx)
		txn.Abort()
		if !assert.NoError(t, err) {
			continue
		}
		assert.Nil(t, node)

		pctx := jctx.PodSchedulingContext
		require.NotNil(t, pctx)
		assert.Equal(t, "", pctx.NodeId)
		assert.Equal(t, 1, len(pctx.NumExcludedNodesByReason))
	}
}

func TestGetNodes(t *testing.T) {
	node1 := testfixtures.Test8GpuNode(testfixtures.TestPriorities)
	node2 := testfixtures.Test8GpuNode(testfixtures.TestPriorities)
	nodeDb, err := newNodeDbWithNodes([]*internaltypes.Node{node1, node2})
	require.NoError(t, err)
	nodes, err := nodeDb.GetNodes()
	require.NoError(t, err)

	assert.Len(t, nodes, 2)
	assert.True(t, slices.Contains(nodes, node1))
	assert.True(t, slices.Contains(nodes, node2))
}

func TestGetNodesWithTxn(t *testing.T) {
	node1 := testfixtures.Test8GpuNode(testfixtures.TestPriorities)
	node2 := testfixtures.Test8GpuNode(testfixtures.TestPriorities)
	nodeDb, err := newNodeDbWithNodes([]*internaltypes.Node{node1, node2})
	require.NoError(t, err)
	txn := nodeDb.Txn(true)
	nodes, err := nodeDb.GetNodesWithTxn(txn)
	require.NoError(t, err)

	assert.Len(t, nodes, 2)
	assert.True(t, slices.Contains(nodes, node1))
	assert.True(t, slices.Contains(nodes, node2))
}

func TestNodeBindingEvictionUnbinding(t *testing.T) {
	node := testfixtures.Test8GpuNode(testfixtures.TestPriorities)
	nodeDb, err := newNodeDbWithNodes([]*internaltypes.Node{node})
	require.NoError(t, err)
	entry, err := nodeDb.GetNode(node.GetId())
	require.NoError(t, err)

	job := testfixtures.Test1GpuJob("A", testfixtures.PriorityClass0)
	request := job.KubernetesResourceRequirements()

	jobId := job.Id()

	boundNode, err := nodeDb.BindJobToNode(entry, job, job.PriorityClass().Priority)
	require.NoError(t, err)

	unboundNode, err := nodeDb.UnbindJobFromNode(job, boundNode)
	require.NoError(t, err)

	unboundMultipleNode, err := nodeDb.UnbindJobsFromNode([]*jobdb.Job{job}, boundNode)
	require.NoError(t, err)

	evictedNode, err := nodeDb.EvictJobsFromNode([]*jobdb.Job{job}, boundNode)
	require.NoError(t, err)

	evictedUnboundNode, err := nodeDb.UnbindJobFromNode(job, evictedNode)
	require.NoError(t, err)

	evictedBoundNode, err := nodeDb.BindJobToNode(evictedNode, job, job.PriorityClass().Priority)
	require.NoError(t, err)

	_, err = nodeDb.EvictJobsFromNode([]*jobdb.Job{job}, entry)
	require.Error(t, err)

	_, err = nodeDb.UnbindJobFromNode(job, entry)
	require.NoError(t, err)

	_, err = nodeDb.BindJobToNode(boundNode, job, job.PriorityClass().Priority)
	require.Error(t, err)

	_, err = nodeDb.EvictJobsFromNode([]*jobdb.Job{job}, evictedNode)
	require.Error(t, err)

	assertNodeAccountingEqual(t, entry, unboundNode)
	assertNodeAccountingEqual(t, entry, evictedUnboundNode)
	assertNodeAccountingEqual(t, unboundNode, evictedUnboundNode)
	assertNodeAccountingEqual(t, boundNode, evictedBoundNode)
	assertNodeAccountingEqual(t, unboundNode, unboundMultipleNode)

	assert.True(
		t,
		armadamaps.DeepEqual(
			map[string]internaltypes.ResourceList{jobId: request},
			boundNode.AllocatedByJobId,
		),
	)
	assert.True(
		t,
		armadamaps.DeepEqual(
			map[string]internaltypes.ResourceList{jobId: request},
			evictedNode.AllocatedByJobId,
		),
	)

	assert.True(
		t,
		armadamaps.DeepEqual(
			map[string]internaltypes.ResourceList{"A": request},
			boundNode.AllocatedByQueue,
		),
	)
	assert.True(
		t,
		armadamaps.DeepEqual(
			map[string]internaltypes.ResourceList{"A": request},
			evictedNode.AllocatedByQueue,
		),
	)

	expectedAllocatable := boundNode.GetTotalResources()
	expectedAllocatable = expectedAllocatable.Subtract(request)
	priority := testfixtures.TestPriorityClasses[job.PriorityClassName()].Priority
	assert.True(t, expectedAllocatable.Equal(boundNode.AllocatableByPriority[priority]))

	assert.Empty(t, unboundNode.AllocatedByJobId)
	assert.Empty(t, unboundNode.AllocatedByQueue)
	assert.Empty(t, unboundNode.EvictedJobRunIds)
}

func assertNodeAccountingEqual(t *testing.T, node1, node2 *internaltypes.Node) {
	assert.True(
		t,
		armadamaps.DeepEqual(node1.AllocatableByPriority, node2.AllocatableByPriority),
		"expected %v, but got %v",
		node1.AllocatableByPriority,
		node2.AllocatableByPriority,
	)
	assert.True(
		t,
		armadamaps.DeepEqual(
			node1.AllocatedByJobId,
			node2.AllocatedByJobId,
		),
		"expected %v, but got %v",
		node1.AllocatedByJobId,
		node2.AllocatedByJobId,
	)
	assert.True(
		t,
		armadamaps.DeepEqual(
			node1.AllocatedByQueue,
			node2.AllocatedByQueue,
		),
		"expected %v, but got %v",
		node1.AllocatedByQueue,
		node2.AllocatedByQueue,
	)
	assert.True(
		t,
		maps.Equal(
			node1.EvictedJobRunIds,
			node2.EvictedJobRunIds,
		),
		"expected %v, but got %v",
		node1.EvictedJobRunIds,
		node2.EvictedJobRunIds,
	)
}

func TestEviction(t *testing.T) {
	nodeDb, err := newNodeDbWithNodes([]*internaltypes.Node{})
	require.NoError(t, err)

	txn := nodeDb.Txn(true)
	jobs := []*jobdb.Job{
		testfixtures.Test1Cpu4GiJob("queue-alice", testfixtures.PriorityClass0),
		testfixtures.Test1Cpu4GiJob("queue-alice", testfixtures.PriorityClass3),
	}

	node := testfixtures.Test32CpuNode(testfixtures.TestPriorities)
	require.NoError(t, err)
	err = nodeDb.CreateAndInsertWithJobDbJobsWithTxn(txn, jobs, node)
	txn.Commit()
	require.NoError(t, err)

	node, err = nodeDb.GetNode(node.GetId())
	require.NoError(t, err)
	assert.Equal(t, 0, len(node.EvictedJobRunIds))
	assert.Equal(t, map[int32]internaltypes.ResourceList{
		-1:    testfixtures.CpuMem("30", "248Gi"),
		0:     testfixtures.CpuMem("30", "248Gi"),
		1:     testfixtures.CpuMem("31", "252Gi"),
		2:     testfixtures.CpuMem("31", "252Gi"),
		3:     testfixtures.CpuMem("31", "252Gi"),
		28000: testfixtures.CpuMem("32", "256Gi"),
		29000: testfixtures.CpuMem("32", "256Gi"),
		30000: testfixtures.CpuMem("32", "256Gi"),
	}, node.AllocatableByPriority)

	returnedNode, err := nodeDb.EvictJobsFromNode(jobs, node)
	assert.Nil(t, err)
	assert.Equal(t, 0, len(node.EvictedJobRunIds))
	assert.Equal(t, len(jobs), len(returnedNode.EvictedJobRunIds))

	assert.Equal(t, map[int32]internaltypes.ResourceList{
		-1:    testfixtures.CpuMem("30", "248Gi"),
		0:     testfixtures.CpuMem("30", "248Gi"),
		1:     testfixtures.CpuMem("31", "252Gi"),
		2:     testfixtures.CpuMem("31", "252Gi"),
		3:     testfixtures.CpuMem("31", "252Gi"),
		28000: testfixtures.CpuMem("32", "256Gi"),
		29000: testfixtures.CpuMem("32", "256Gi"),
		30000: testfixtures.CpuMem("32", "256Gi"),
	}, node.AllocatableByPriority)

	assert.Equal(t, map[int32]internaltypes.ResourceList{
		-1:    testfixtures.CpuMem("30", "248Gi"),
		0:     testfixtures.CpuMem("32", "256Gi"),
		1:     testfixtures.CpuMem("32", "256Gi"),
		2:     testfixtures.CpuMem("32", "256Gi"),
		3:     testfixtures.CpuMem("32", "256Gi"),
		28000: testfixtures.CpuMem("32", "256Gi"),
		29000: testfixtures.CpuMem("32", "256Gi"),
		30000: testfixtures.CpuMem("32", "256Gi"),
	}, returnedNode.AllocatableByPriority)
}

func TestScheduleIndividually(t *testing.T) {
	tests := map[string]struct {
		Nodes         []*internaltypes.Node
		Jobs          []*jobdb.Job
		ExpectSuccess []bool
	}{
		"all jobs fit": {
			Nodes:         testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Jobs:          testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 32),
			ExpectSuccess: testfixtures.Repeat(true, 32),
		},
		"not all jobs fit": {
			Nodes:         testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Jobs:          testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 33),
			ExpectSuccess: append(testfixtures.Repeat(true, 32), testfixtures.Repeat(false, 1)...),
		},
		"unavailable resource": {
			Nodes:         testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Jobs:          testfixtures.N1GpuJobs("A", testfixtures.PriorityClass0, 1),
			ExpectSuccess: testfixtures.Repeat(false, 1),
		},
		"unsupported resource": {
			Nodes: testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Jobs: testfixtures.WithRequestsJobs(
				schedulerobjects.ResourceList{
					Resources: map[string]*resource.Quantity{
						"gibberish": pointer.MustParseResource("1"),
					},
				},
				testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1),
			),
			ExpectSuccess: testfixtures.Repeat(true, 1), // we ignore unknown resource types on jobs, should never happen in practice anyway as these should fail earlier.
		},
		"preemption": {
			Nodes: testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Jobs: append(
				append(
					testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 32),
					testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass1, 32)...,
				),
				testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 32)...,
			),
			ExpectSuccess: append(testfixtures.Repeat(true, 64), testfixtures.Repeat(false, 32)...),
		},
		"taints/tolerations": {
			Nodes: testfixtures.NTainted32CpuNodes(1, testfixtures.TestPriorities),
			Jobs: append(
				append(
					testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1),
					testfixtures.N1GpuJobs("A", testfixtures.PriorityClass0, 1)...,
				),
				testfixtures.N32Cpu256GiJobsWithLargeJobToleration("A", testfixtures.PriorityClass0, 1)...,
			),
			ExpectSuccess: []bool{false, false, true},
		},
		"node selector": {
			Nodes: append(testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
				testfixtures.TestNodeFactory.AddLabels(
					testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
					map[string]string{
						"key": "value",
					},
				)...),
			Jobs: testfixtures.WithNodeSelectorJobs(
				map[string]string{
					"key": "value",
				},
				testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 33),
			),
			ExpectSuccess: append(testfixtures.Repeat(true, 32), testfixtures.Repeat(false, 1)...),
		},
		"node selector with mismatched value": {
			Nodes: testfixtures.TestNodeFactory.AddLabels(
				testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
				map[string]string{
					"key": "value",
				},
			),
			Jobs: testfixtures.WithNodeSelectorJobs(
				map[string]string{
					"key": "this is the wrong value",
				},
				testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1),
			),
			ExpectSuccess: testfixtures.Repeat(false, 1),
		},
		"node selector with missing label": {
			Nodes: testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Jobs: testfixtures.WithNodeSelectorJobs(
				map[string]string{
					"this label does not exist": "value",
				},
				testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1),
			),
			ExpectSuccess: testfixtures.Repeat(false, 1),
		},
		"node affinity": {
			Nodes: append(
				testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
				testfixtures.TestNodeFactory.AddLabels(
					testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
					map[string]string{
						"key": "value",
					},
				)...,
			),
			Jobs: testfixtures.WithNodeAffinityJobs(
				[]v1.NodeSelectorTerm{
					{
						MatchExpressions: []v1.NodeSelectorRequirement{
							{
								Key:      "key",
								Operator: v1.NodeSelectorOpIn,
								Values:   []string{"value"},
							},
						},
					},
				},
				testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 33),
			),
			ExpectSuccess: append(testfixtures.Repeat(true, 32), testfixtures.Repeat(false, 1)...),
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			nodeDb, err := newNodeDbWithNodes(tc.Nodes)
			require.NoError(t, err)

			jctxs := context.JobSchedulingContextsFromJobs(tc.Jobs)

			for i, jctx := range jctxs {
				nodeDbTxn := nodeDb.Txn(true)
				gctx := context.NewGangSchedulingContext([]*context.JobSchedulingContext{jctx})
				ok, err := nodeDb.ScheduleManyWithTxn(nodeDbTxn, gctx)
				require.NoError(t, err)

				require.Equal(t, tc.ExpectSuccess[i], ok)

				pctx := jctx.PodSchedulingContext

				if !ok {
					nodeDbTxn.Abort()
					if pctx != nil {
						assert.Equal(t, "", pctx.NodeId)
					}
					continue
				}

				nodeDbTxn.Commit()

				require.NotNil(t, pctx)
				nodeId := pctx.NodeId
				require.NotEqual(t, "", nodeId)
				job := jctx.Job
				node, err := nodeDb.GetNode(nodeId)
				require.NoError(t, err)
				require.NotNil(t, node)
				expected := job.KubernetesResourceRequirements()
				actual, ok := node.AllocatedByJobId[job.Id()]
				require.True(t, ok)
				assert.True(t, actual.Equal(expected))
			}
		})
	}
}

func TestScheduleMany(t *testing.T) {
	gangSuccess := testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 32))
	gangFailure := testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 33))

	tests := map[string]struct {
		// Nodes to schedule across.
		Nodes []*internaltypes.Node
		// Schedule one group of jobs at a time.
		// Each group is composed of a slice of pods.
		Jobs [][]*jobdb.Job
		// For each group, whether we expect scheduling to succeed.
		ExpectSuccess []bool
	}{
		// Attempts to schedule 32. All jobs get scheduled.
		"simple success": {
			Nodes:         testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Jobs:          [][]*jobdb.Job{gangSuccess},
			ExpectSuccess: []bool{true},
		},
		// Attempts to schedule 33 jobs. The overall result fails.
		"simple failure with min cardinality": {
			Nodes:         testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Jobs:          [][]*jobdb.Job{gangFailure},
			ExpectSuccess: []bool{false},
		},
		"correct rollback": {
			Nodes: testfixtures.N32CpuNodes(2, testfixtures.TestPriorities),
			Jobs: [][]*jobdb.Job{
				gangSuccess,
				gangFailure,
				gangSuccess,
			},
			ExpectSuccess: []bool{true, false, true},
		},
		"varying job size": {
			Nodes: testfixtures.N32CpuNodes(2, testfixtures.TestPriorities),
			Jobs: [][]*jobdb.Job{
				append(
					testfixtures.N32Cpu256GiJobsWithLargeJobToleration("A", testfixtures.PriorityClass0, 1),
					testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 32)...,
				),
				testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1),
			},
			ExpectSuccess: []bool{true, false},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			nodeDb, err := newNodeDbWithNodes(tc.Nodes)
			require.NoError(t, err)
			for i, jobs := range tc.Jobs {
				nodeDbTxn := nodeDb.Txn(true)
				jctxs := context.JobSchedulingContextsFromJobs(jobs)
				gctx := context.NewGangSchedulingContext(jctxs)
				ok, err := nodeDb.ScheduleManyWithTxn(nodeDbTxn, gctx)
				require.NoError(t, err)
				require.Equal(t, tc.ExpectSuccess[i], ok)
				if ok {
					nodeDbTxn.Commit()
				} else {
					nodeDbTxn.Abort()
					// We make no assertions about pctx in this case; if some of
					// the jobs in the gang were scheduled successfully and
					// others were not, then pctx.NodeId will be inconsistent
					// until the gang is returned back to the gang scheduler.
					continue
				}
				for _, jctx := range jctxs {
					pctx := jctx.PodSchedulingContext
					require.NotNil(t, pctx)
					assert.NotEqual(t, "", pctx.NodeId)
				}
			}
		})
	}
}

func TestAwayNodeScheduling(t *testing.T) {
	tests := map[string]struct {
		shouldSubmitGang bool
		expectSuccess    bool
	}{
		"should schedule away jobs": {
			shouldSubmitGang: false,
			expectSuccess:    true,
		},
		"should schedule not schedule gangs as away jobs": {
			shouldSubmitGang: true,
			expectSuccess:    false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			nodeDb, err := NewNodeDb(
				testfixtures.TestPriorityClasses,
				testfixtures.TestResources,
				testfixtures.TestIndexedTaints,
				testfixtures.TestIndexedNodeLabels,
				testfixtures.TestWellKnownNodeTypes,
				testfixtures.TestResourceListFactory,
			)
			require.NoError(t, err)

			nodeDbTxn := nodeDb.Txn(true)
			node := testfixtures.Test32CpuNode([]int32{29000, 30000})
			node = testfixtures.TestNodeFactory.AddTaints(
				[]*internaltypes.Node{node},
				[]v1.Taint{
					{
						Key:    "gpu",
						Value:  "true",
						Effect: v1.TaintEffectNoSchedule,
					},
				},
			)[0]

			jobId := util.ULID()
			job := testfixtures.TestJob(
				testfixtures.TestQueue, jobId,
				"armada-preemptible-away",
				testfixtures.Test1Cpu4GiPodReqs(testfixtures.TestQueue, jobId, 30000),
			)
			if tc.shouldSubmitGang {
				job = testfixtures.WithGangAnnotationsJobs([]*jobdb.Job{job.DeepCopy(), job.DeepCopy()})[0]
			}

			jctx := context.JobSchedulingContextFromJob(job)
			assert.Equal(t, tc.shouldSubmitGang, jctx.IsGang)

			require.NoError(t, nodeDb.CreateAndInsertWithJobDbJobsWithTxn(nodeDbTxn, nil, node))

			require.Empty(t, jctx.AdditionalTolerations)
			gctx := context.NewGangSchedulingContext([]*context.JobSchedulingContext{jctx})

			ok, err := nodeDb.ScheduleManyWithTxn(nodeDbTxn, gctx)
			require.NoError(t, err)
			if tc.expectSuccess {
				require.True(t, ok)
				assert.NotNil(t, jctx.PodSchedulingContext)
				assert.True(t, jctx.PodSchedulingContext.IsSuccessful())
				assert.Equal(t, node.GetId(), jctx.PodSchedulingContext.NodeId)
				assert.Equal(t, context.ScheduledAsAwayJob, jctx.PodSchedulingContext.SchedulingMethod)
				assert.Equal(t, int32(29000), jctx.PodSchedulingContext.ScheduledAtPriority)
				require.Equal(
					t,
					[]v1.Toleration{
						{
							Key:    "gpu",
							Value:  "true",
							Effect: v1.TaintEffectNoSchedule,
						},
					},
					jctx.AdditionalTolerations,
				)
			} else {
				require.False(t, ok)
				assert.NotNil(t, jctx.PodSchedulingContext)
				assert.False(t, jctx.PodSchedulingContext.IsSuccessful())
				assert.Empty(t, jctx.PodSchedulingContext.NodeId)
				assert.Empty(t, jctx.AdditionalTolerations)
			}
		})
	}
}

func TestMakeIndexedResourceResolution(t *testing.T) {
	supportedResources := []schedulerconfig.ResourceType{
		{
			Name:       "unit-resource-1",
			Resolution: resource.MustParse("1"),
		},
		{
			Name:       "unit-resource-2",
			Resolution: resource.MustParse("1"),
		},
		{
			Name:       "un-indexed-resource",
			Resolution: resource.MustParse("1"),
		},
		{
			Name:       "milli-resource-1",
			Resolution: resource.MustParse("1m"),
		},
		{
			Name:       "milli-resource-2",
			Resolution: resource.MustParse("1m"),
		},
	}

	indexedResources := []schedulerconfig.ResourceType{
		{
			Name:       "unit-resource-1",
			Resolution: resource.MustParse("1"),
		},
		{
			Name:       "unit-resource-2",
			Resolution: resource.MustParse("100"),
		},
		{
			Name:       "milli-resource-1",
			Resolution: resource.MustParse("1m"),
		},
		{
			Name:       "milli-resource-2",
			Resolution: resource.MustParse("1"),
		},
	}

	resourceListFactory, err := internaltypes.NewResourceListFactory(supportedResources, nil)
	assert.Nil(t, err)
	assert.NotNil(t, resourceListFactory)

	result, err := makeIndexedResourceResolution(indexedResources, resourceListFactory)
	assert.Nil(t, err)
	assert.Equal(t, []int64{1, 100, 1, 1000}, result)
}

func TestMakeIndexedResourceResolution_ErrorsOnUnsupportedResource(t *testing.T) {
	supportedResources := []schedulerconfig.ResourceType{
		{
			Name:       "a-resource",
			Resolution: resource.MustParse("1"),
		},
	}

	indexedResources := []schedulerconfig.ResourceType{
		{
			Name:       "non-supported-resource",
			Resolution: resource.MustParse("1"),
		},
	}

	resourceListFactory, err := internaltypes.NewResourceListFactory(supportedResources, nil)
	assert.Nil(t, err)
	assert.NotNil(t, resourceListFactory)

	result, err := makeIndexedResourceResolution(indexedResources, resourceListFactory)
	assert.NotNil(t, err)
	assert.Nil(t, result)
}

func TestMakeIndexedResourceResolution_ErrorsOnInvalidResolution(t *testing.T) {
	supportedResources := []schedulerconfig.ResourceType{
		{
			Name:       "a-resource",
			Resolution: resource.MustParse("1"),
		},
	}

	resourceListFactory, err := internaltypes.NewResourceListFactory(supportedResources, nil)
	assert.Nil(t, err)
	assert.NotNil(t, resourceListFactory)

	result, err := makeIndexedResourceResolution([]schedulerconfig.ResourceType{
		{
			Name:       "a-resource",
			Resolution: resource.MustParse("0"),
		},
	}, resourceListFactory)
	assert.NotNil(t, err)
	assert.Nil(t, result)

	result, err = makeIndexedResourceResolution([]schedulerconfig.ResourceType{
		{
			Name:       "a-resource",
			Resolution: resource.MustParse("-1"),
		},
	}, resourceListFactory)
	assert.NotNil(t, err)
	assert.Nil(t, result)

	result, err = makeIndexedResourceResolution([]schedulerconfig.ResourceType{
		{
			Name:       "a-resource",
			Resolution: resource.MustParse("0.1"), // this cannot be less than the supported resource type resolution, should error
		},
	}, resourceListFactory)
	assert.NotNil(t, err)
	assert.Nil(t, result)
}

func benchmarkUpsert(nodes []*internaltypes.Node, b *testing.B) {
	nodeDb, err := NewNodeDb(
		testfixtures.TestPriorityClasses,
		testfixtures.TestResources,
		testfixtures.TestIndexedTaints,
		testfixtures.TestIndexedNodeLabels,
		testfixtures.TestWellKnownNodeTypes,
		testfixtures.TestResourceListFactory,
	)
	require.NoError(b, err)
	txn := nodeDb.Txn(true)
	entries := make([]*internaltypes.Node, len(nodes))
	for i, node := range nodes {
		err = nodeDb.CreateAndInsertWithJobDbJobsWithTxn(txn, nil, node)
		require.NoError(b, err)
		entry, err := nodeDb.GetNodeWithTxn(txn, node.GetId())
		require.NotNil(b, entry)
		require.NoError(b, err)
		entries[i] = entry
	}
	txn.Commit()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		err := nodeDb.UpsertMany(entries)
		require.NoError(b, err)
	}
}

func BenchmarkUpsert1(b *testing.B) {
	benchmarkUpsert(testfixtures.N32CpuNodes(1, testfixtures.TestPriorities), b)
}

func BenchmarkUpsert1000(b *testing.B) {
	benchmarkUpsert(testfixtures.N32CpuNodes(1000, testfixtures.TestPriorities), b)
}

func BenchmarkUpsert100000(b *testing.B) {
	benchmarkUpsert(testfixtures.N32CpuNodes(100000, testfixtures.TestPriorities), b)
}

func benchmarkScheduleMany(b *testing.B, nodes []*internaltypes.Node, jobs []*jobdb.Job) {
	nodeDb, err := NewNodeDb(
		testfixtures.TestPriorityClasses,
		testfixtures.TestResources,
		testfixtures.TestIndexedTaints,
		testfixtures.TestIndexedNodeLabels,
		testfixtures.TestWellKnownNodeTypes,
		testfixtures.TestResourceListFactory,
	)
	require.NoError(b, err)
	txn := nodeDb.Txn(true)
	for _, node := range nodes {
		err = nodeDb.CreateAndInsertWithJobDbJobsWithTxn(txn, nil, node)
		require.NoError(b, err)
	}
	txn.Commit()

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		jctxs := context.JobSchedulingContextsFromJobs(jobs)
		gctx := context.NewGangSchedulingContext(jctxs)
		txn := nodeDb.Txn(true)
		_, err := nodeDb.ScheduleManyWithTxn(txn, gctx)
		txn.Abort()
		require.NoError(b, err)
	}
}

func BenchmarkScheduleMany10CpuNodes320SmallJobs(b *testing.B) {
	benchmarkScheduleMany(
		b,
		testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
		testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 320),
	)
}

func BenchmarkScheduleMany10CpuNodes640SmallJobs(b *testing.B) {
	benchmarkScheduleMany(
		b,
		testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
		testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 640),
	)
}

func BenchmarkScheduleMany100CpuNodes3200SmallJobs(b *testing.B) {
	benchmarkScheduleMany(
		b,
		testfixtures.N32CpuNodes(100, testfixtures.TestPriorities),
		testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 3200),
	)
}

func BenchmarkScheduleMany100CpuNodes6400SmallJobs(b *testing.B) {
	benchmarkScheduleMany(
		b,
		testfixtures.N32CpuNodes(100, testfixtures.TestPriorities),
		testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 6400),
	)
}

func BenchmarkScheduleMany1000CpuNodes32000SmallJobs(b *testing.B) {
	benchmarkScheduleMany(
		b,
		testfixtures.N32CpuNodes(1000, testfixtures.TestPriorities),
		testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 32000),
	)
}

func BenchmarkScheduleMany1000CpuNodes64000SmallJobs(b *testing.B) {
	benchmarkScheduleMany(
		b,
		testfixtures.N32CpuNodes(1000, testfixtures.TestPriorities),
		testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 64000),
	)
}

func BenchmarkScheduleMany100CpuNodes1CpuUnused(b *testing.B) {
	benchmarkScheduleMany(
		b,
		testfixtures.WithUsedResourcesNodes(
			0,
			testfixtures.Cpu("31"),
			testfixtures.N32CpuNodes(100, testfixtures.TestPriorities),
		),
		testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 100),
	)
}

func BenchmarkScheduleMany1000CpuNodes1CpuUnused(b *testing.B) {
	benchmarkScheduleMany(
		b,
		testfixtures.WithUsedResourcesNodes(
			0,
			testfixtures.Cpu("31"),
			testfixtures.N32CpuNodes(1000, testfixtures.TestPriorities),
		),
		testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1000),
	)
}

func BenchmarkScheduleMany10000CpuNodes1CpuUnused(b *testing.B) {
	benchmarkScheduleMany(
		b,
		testfixtures.WithUsedResourcesNodes(
			0,
			testfixtures.Cpu("31"),
			testfixtures.N32CpuNodes(10000, testfixtures.TestPriorities),
		),
		testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 10000),
	)
}

func BenchmarkScheduleManyResourceConstrained(b *testing.B) {
	nodes := append(append(
		testfixtures.N32CpuNodes(500, testfixtures.TestPriorities),
		testfixtures.N8GpuNodes(1, testfixtures.TestPriorities)...),
		testfixtures.N32CpuNodes(499, testfixtures.TestPriorities)...,
	)
	benchmarkScheduleMany(
		b,
		nodes,
		testfixtures.N1GpuJobs("A", testfixtures.PriorityClass0, 1),
	)
}

func newNodeDbWithNodes(nodes []*internaltypes.Node) (*NodeDb, error) {
	nodeDb, err := NewNodeDb(
		testfixtures.TestPriorityClasses,
		testfixtures.TestResources,
		testfixtures.TestIndexedTaints,
		testfixtures.TestIndexedNodeLabels,
		testfixtures.TestWellKnownNodeTypes,
		testfixtures.TestResourceListFactory,
	)
	if err != nil {
		return nil, err
	}
	txn := nodeDb.Txn(true)
	for _, node := range nodes {
		if err = nodeDb.CreateAndInsertWithJobDbJobsWithTxn(txn, nil, node); err != nil {
			return nil, err
		}
	}
	txn.Commit()
	return nodeDb, nil
}

func BenchmarkNodeDbStringFromPodRequirementsNotMetReason(b *testing.B) {
	nodeDb := &NodeDb{
		podRequirementsNotMetReasonStringCache: make(map[uint64]string, 128),
	}
	reason := &UntoleratedTaint{
		Taint: v1.Taint{Key: randomString(100), Value: randomString(100), Effect: v1.TaintEffectNoSchedule},
	}
	nodeDb.stringFromPodRequirementsNotMetReason(reason)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		nodeDb.stringFromPodRequirementsNotMetReason(reason)
	}
}

func randomString(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += fmt.Sprint(i)
	}
	return s
}
