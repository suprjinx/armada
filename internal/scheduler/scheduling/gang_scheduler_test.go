package scheduling

import (
	"fmt"
	"testing"

	"github.com/oklog/ulid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/maps"
	"golang.org/x/time/rate"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/armadaproject/armada/internal/common/armadacontext"
	"github.com/armadaproject/armada/internal/common/pointer"
	armadaslices "github.com/armadaproject/armada/internal/common/slices"
	"github.com/armadaproject/armada/internal/common/types"
	"github.com/armadaproject/armada/internal/common/util"
	"github.com/armadaproject/armada/internal/scheduler/configuration"
	"github.com/armadaproject/armada/internal/scheduler/floatingresources"
	"github.com/armadaproject/armada/internal/scheduler/internaltypes"
	"github.com/armadaproject/armada/internal/scheduler/jobdb"
	"github.com/armadaproject/armada/internal/scheduler/nodedb"
	"github.com/armadaproject/armada/internal/scheduler/schedulerobjects"
	schedulerconstraints "github.com/armadaproject/armada/internal/scheduler/scheduling/constraints"
	"github.com/armadaproject/armada/internal/scheduler/scheduling/context"
	"github.com/armadaproject/armada/internal/scheduler/scheduling/fairness"
	"github.com/armadaproject/armada/internal/scheduler/testfixtures"
	"github.com/armadaproject/armada/pkg/api"
)

func TestGangScheduler(t *testing.T) {
	tests := map[string]struct {
		SchedulingConfig configuration.SchedulingConfig
		// Nodes to be considered by the scheduler.
		Nodes []*internaltypes.Node
		// Total resources across all clusters.
		// Set to the total resources across all nodes if not provided.
		TotalResources internaltypes.ResourceList
		// Gangs to try scheduling.
		Gangs [][]*jobdb.Job
		// Indices of gangs expected to be scheduled.
		ExpectedScheduledIndices []int
		// Cumulative number of jobs we expect to schedule successfully.
		// Each index `i` is the expected value when processing gang `i`.
		ExpectedCumulativeScheduledJobs []int
		// If present, assert that gang `i` is scheduled on nodes with node
		// uniformity label `ExpectedNodeUniformity[i]`.
		ExpectedNodeUniformity map[int]string
		// The expected number of jobs we successfully scheduled between min gang cardinality and gang cardinality.
		ExpectedRuntimeGangCardinality []int
		// The scheduling keys expected to have been marked as unfeasible by the end of the test.
		ExpectedUnfeasibleSchedulingKeyIndices []int
	}{
		"simple success": {
			SchedulingConfig: testfixtures.TestSchedulingConfig(),
			Nodes:            testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 32)),
			},
			ExpectedScheduledIndices:               testfixtures.IntRange(0, 0),
			ExpectedCumulativeScheduledJobs:        []int{32},
			ExpectedRuntimeGangCardinality:         []int{32},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"simple failure": {
			SchedulingConfig: testfixtures.TestSchedulingConfig(),
			Nodes:            testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 33)),
			},
			ExpectedScheduledIndices:               nil,
			ExpectedCumulativeScheduledJobs:        []int{0},
			ExpectedRuntimeGangCardinality:         []int{0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"one success and one failure": {
			SchedulingConfig: testfixtures.TestSchedulingConfig(),
			Nodes:            testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 32)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
			},
			ExpectedScheduledIndices:               testfixtures.IntRange(0, 0),
			ExpectedCumulativeScheduledJobs:        []int{32, 32},
			ExpectedRuntimeGangCardinality:         []int{32, 0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{1},
		},
		"multiple nodes": {
			SchedulingConfig: testfixtures.TestSchedulingConfig(),
			Nodes:            testfixtures.N32CpuNodes(2, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 64)),
			},
			ExpectedScheduledIndices:               testfixtures.IntRange(0, 0),
			ExpectedCumulativeScheduledJobs:        []int{64},
			ExpectedRuntimeGangCardinality:         []int{64},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"floating resources": {
			SchedulingConfig: func() configuration.SchedulingConfig {
				cfg := testfixtures.TestSchedulingConfig()
				cfg.FloatingResources = testfixtures.TestFloatingResourceConfig
				return cfg
			}(),
			Nodes: testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				// we have 10 of test-floating-resource so only the first of these two jobs should fit
				addFloatingResourceRequest("6", testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1))),
				addFloatingResourceRequest("6", testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1))),
			},
			ExpectedScheduledIndices:               testfixtures.IntRange(0, 0),
			ExpectedCumulativeScheduledJobs:        []int{1, 1},
			ExpectedRuntimeGangCardinality:         []int{1, 0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"MaximumResourceFractionToSchedule": {
			SchedulingConfig: testfixtures.WithRoundLimitsConfig(
				map[string]float64{"cpu": 0.5},
				testfixtures.TestSchedulingConfig(),
			),
			Nodes: testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 8)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 16)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 8)),
			},
			ExpectedScheduledIndices:               []int{0, 1},
			ExpectedCumulativeScheduledJobs:        []int{8, 24, 24},
			ExpectedRuntimeGangCardinality:         []int{8, 16, 0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"MaximumResourceFractionToScheduleByPool": {
			SchedulingConfig: testfixtures.WithRoundLimitsConfig(
				map[string]float64{"cpu": 0.5},
				testfixtures.WithRoundLimitsPoolConfig(
					map[string]map[string]float64{"pool": {"cpu": 2.0 / 32.0}},
					testfixtures.TestSchedulingConfig(),
				),
			),
			Nodes: testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
			},
			ExpectedScheduledIndices:               []int{0, 1, 2},
			ExpectedCumulativeScheduledJobs:        []int{1, 2, 3, 3, 3},
			ExpectedRuntimeGangCardinality:         []int{1, 1, 1, 0, 0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"MaximumResourceFractionToScheduleByPool non-existing pool": {
			SchedulingConfig: testfixtures.WithRoundLimitsConfig(
				map[string]float64{"cpu": 3.0 / 32.0},
				testfixtures.WithRoundLimitsPoolConfig(
					map[string]map[string]float64{"this does not exist": {"cpu": 2.0 / 32.0}},
					testfixtures.TestSchedulingConfig(),
				),
			),
			Nodes: testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
			},
			ExpectedScheduledIndices:               []int{0, 1, 2, 3},
			ExpectedCumulativeScheduledJobs:        []int{1, 2, 3, 4, 4},
			ExpectedRuntimeGangCardinality:         []int{1, 1, 1, 1, 0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"MaximumResourceFractionPerQueue": {
			SchedulingConfig: testfixtures.WithPerPriorityLimitsConfig(
				map[string]map[string]float64{
					testfixtures.PriorityClass0: {"cpu": 1.0 / 32.0},
					testfixtures.PriorityClass1: {"cpu": 2.0 / 32.0},
					testfixtures.PriorityClass2: {"cpu": 3.0 / 32.0},
					testfixtures.PriorityClass3: {"cpu": 4.0 / 32.0},
				},
				testfixtures.TestSchedulingConfig(),
			),
			Nodes: testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 2)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass1, 2)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass1, 3)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass2, 3)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass2, 4)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass3, 4)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass3, 5)),
			},
			ExpectedScheduledIndices:               []int{0, 2, 4, 6},
			ExpectedCumulativeScheduledJobs:        []int{1, 1, 3, 3, 6, 6, 10, 10},
			ExpectedRuntimeGangCardinality:         []int{1, 0, 2, 0, 3, 0, 4, 0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"resolution has no impact on jobs of size a multiple of the resolution": {
			SchedulingConfig: testfixtures.WithIndexedResourcesConfig(
				[]configuration.ResourceType{
					{Name: "cpu", Resolution: resource.MustParse("16")},
					{Name: "memory", Resolution: resource.MustParse("128Mi")},
				},
				testfixtures.TestSchedulingConfig(),
			),
			Nodes: testfixtures.N32CpuNodes(3, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 1)),
			},
			ExpectedScheduledIndices:               testfixtures.IntRange(0, 5),
			ExpectedCumulativeScheduledJobs:        testfixtures.IntRange(1, 6),
			ExpectedRuntimeGangCardinality:         []int{1, 1, 1, 1, 1, 1},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"jobs of size not a multiple of the resolution blocks scheduling new jobs": {
			SchedulingConfig: testfixtures.WithIndexedResourcesConfig(
				[]configuration.ResourceType{
					{Name: "cpu", Resolution: resource.MustParse("17")},
					{Name: "memory", Resolution: resource.MustParse("128Mi")},
				},
				testfixtures.TestSchedulingConfig(),
			),
			Nodes: testfixtures.N32CpuNodes(3, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 1)),
			},
			ExpectedScheduledIndices:               testfixtures.IntRange(0, 2),
			ExpectedCumulativeScheduledJobs:        []int{1, 2, 3, 3},
			ExpectedRuntimeGangCardinality:         []int{1, 1, 1, 0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{3},
		},
		"consider all nodes in the bucket": {
			SchedulingConfig: testfixtures.WithIndexedResourcesConfig(
				[]configuration.ResourceType{
					{Name: "cpu", Resolution: resource.MustParse("1")},
					{Name: "memory", Resolution: resource.MustParse("1Mi")},
					{Name: "nvidia.com/gpu", Resolution: resource.MustParse("1")},
				},
				testfixtures.TestSchedulingConfig(),
			),
			Nodes: armadaslices.Concatenate(
				testfixtures.WithUsedResourcesNodes(
					0,
					testfixtures.CpuMemGpu("31.5", "512Gi", "8"),
					testfixtures.N8GpuNodes(1, testfixtures.TestPriorities),
				),
				testfixtures.WithUsedResourcesNodes(
					0,
					testfixtures.Cpu("32"),
					testfixtures.N8GpuNodes(1, testfixtures.TestPriorities),
				),
			),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1GpuJobs("A", testfixtures.PriorityClass0, 1)),
			},
			ExpectedScheduledIndices:               testfixtures.IntRange(0, 0),
			ExpectedCumulativeScheduledJobs:        []int{1},
			ExpectedRuntimeGangCardinality:         []int{1},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"NodeUniformityLabel set but not indexed": {
			SchedulingConfig: testfixtures.TestSchedulingConfig(),
			Nodes: testfixtures.TestNodeFactory.AddLabels(
				testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
				map[string]string{"foo": "foov"},
			),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(
					testfixtures.WithNodeUniformityLabelAnnotationJobs(
						"foo",
						testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 2),
					)),
			},
			ExpectedScheduledIndices:               nil,
			ExpectedCumulativeScheduledJobs:        []int{0},
			ExpectedRuntimeGangCardinality:         []int{0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"NodeUniformityLabel not set": {
			SchedulingConfig: testfixtures.WithIndexedNodeLabelsConfig(
				[]string{"foo", "bar"},
				testfixtures.TestSchedulingConfig(),
			),
			Nodes: testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(
					testfixtures.WithNodeUniformityLabelAnnotationJobs(
						"foo",
						testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 2),
					)),
			},
			ExpectedScheduledIndices:               nil,
			ExpectedCumulativeScheduledJobs:        []int{0},
			ExpectedRuntimeGangCardinality:         []int{0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"NodeUniformityLabel insufficient capacity": {
			SchedulingConfig: testfixtures.WithIndexedNodeLabelsConfig(
				[]string{"foo", "bar"},
				testfixtures.TestSchedulingConfig(),
			),
			Nodes: armadaslices.Concatenate(
				testfixtures.TestNodeFactory.AddLabels(
					testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
					map[string]string{"foo": "foov1"},
				),
				testfixtures.TestNodeFactory.AddLabels(
					testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
					map[string]string{"foo": "foov2"},
				),
			),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(
					testfixtures.WithNodeUniformityLabelAnnotationJobs("foo", testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 3)),
				),
			},
			ExpectedScheduledIndices:               nil,
			ExpectedCumulativeScheduledJobs:        []int{0},
			ExpectedRuntimeGangCardinality:         []int{0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"NodeUniformityLabel": {
			SchedulingConfig: testfixtures.WithIndexedNodeLabelsConfig(
				[]string{"foo", "bar"},
				testfixtures.TestSchedulingConfig(),
			),
			Nodes: armadaslices.Concatenate(
				testfixtures.TestNodeFactory.AddLabels(
					testfixtures.WithUsedResourcesNodes(
						0,
						testfixtures.Cpu("1"),
						testfixtures.N32CpuNodes(2, testfixtures.TestPriorities),
					),
					map[string]string{"foo": "foov1"},
				),
				testfixtures.TestNodeFactory.AddLabels(
					testfixtures.N32CpuNodes(2, testfixtures.TestPriorities),
					map[string]string{"foo": "foov2"},
				),
				testfixtures.TestNodeFactory.AddLabels(
					testfixtures.WithUsedResourcesNodes(
						0,
						testfixtures.Cpu("1"),
						testfixtures.N32CpuNodes(2, testfixtures.TestPriorities),
					),
					map[string]string{"foo": "foov3"},
				),
			),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(
					testfixtures.WithNodeUniformityLabelAnnotationJobs(
						"foo",
						testfixtures.N16Cpu128GiJobs("A", testfixtures.PriorityClass0, 4),
					)),
			},
			ExpectedScheduledIndices:               []int{0},
			ExpectedCumulativeScheduledJobs:        []int{4},
			ExpectedRuntimeGangCardinality:         []int{4},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"NodeUniformityLabel NumScheduled tiebreak": {
			SchedulingConfig: testfixtures.WithIndexedNodeLabelsConfig(
				[]string{"my-cool-node-uniformity"},
				testfixtures.TestSchedulingConfig(),
			),
			Nodes: append(
				testfixtures.TestNodeFactory.AddLabels(
					testfixtures.N32CpuNodes(2, testfixtures.TestPriorities),
					map[string]string{"my-cool-node-uniformity": "a"},
				),
				testfixtures.TestNodeFactory.AddLabels(
					testfixtures.N32CpuNodes(3, testfixtures.TestPriorities),
					map[string]string{"my-cool-node-uniformity": "b"},
				)...,
			),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(
					testfixtures.WithNodeUniformityLabelAnnotationJobs(
						"my-cool-node-uniformity",
						testfixtures.N32Cpu256GiJobsWithLargeJobToleration("A", testfixtures.PriorityClass0, 3),
					),
				),
			},
			ExpectedScheduledIndices:               []int{0},
			ExpectedCumulativeScheduledJobs:        []int{3},
			ExpectedNodeUniformity:                 map[int]string{0: "b"},
			ExpectedRuntimeGangCardinality:         []int{3},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"AwayNodeTypes": {
			SchedulingConfig: func() configuration.SchedulingConfig {
				config := testfixtures.TestSchedulingConfig()
				config.PriorityClasses = map[string]types.PriorityClass{
					"armada-preemptible-away": {
						Priority:    30000,
						Preemptible: true,

						AwayNodeTypes: []types.AwayNodeType{
							{Priority: 29000, WellKnownNodeTypeName: "node-type-a"},
							{Priority: 29000, WellKnownNodeTypeName: "node-type-b"},
						},
					},
					"armada-preemptible-away-both": {
						Priority:    30000,
						Preemptible: true,

						AwayNodeTypes: []types.AwayNodeType{
							{Priority: 29000, WellKnownNodeTypeName: "node-type-ab"},
						},
					},
				}
				config.DefaultPriorityClassName = "armada-preemptible-away"
				config.WellKnownNodeTypes = []configuration.WellKnownNodeType{
					{
						Name: "node-type-a",
						Taints: []v1.Taint{
							{Key: "taint-a", Value: "true", Effect: v1.TaintEffectNoSchedule},
						},
					},
					{
						Name: "node-type-b",
						Taints: []v1.Taint{
							{Key: "taint-b", Value: "true", Effect: v1.TaintEffectNoSchedule},
						},
					},
					{
						Name: "node-type-ab",
						Taints: []v1.Taint{
							{Key: "taint-a", Value: "true", Effect: v1.TaintEffectNoSchedule},
							{Key: "taint-b", Value: "true", Effect: v1.TaintEffectNoSchedule},
						},
					},
				}
				return config
			}(),
			Nodes: testfixtures.TestNodeFactory.AddTaints(
				testfixtures.N8GpuNodes(1, []int32{29000, 30000}),
				[]v1.Taint{
					{Key: "taint-a", Value: "true", Effect: v1.TaintEffectNoSchedule},
					{Key: "taint-b", Value: "true", Effect: v1.TaintEffectNoSchedule},
				}),
			Gangs: func() (gangs [][]*jobdb.Job) {
				var jobId ulid.ULID
				jobId = util.ULID()
				gangs = append(gangs, []*jobdb.Job{
					testfixtures.
						TestJob("A", jobId, "armada-preemptible-away", testfixtures.Test1Cpu4GiPodReqs("A", jobId, 30000)),
				})
				jobId = util.ULID()
				gangs = append(gangs, []*jobdb.Job{testfixtures.TestJob("A", jobId, "armada-preemptible-away-both", testfixtures.Test1Cpu4GiPodReqs("A", jobId, 30000))})
				return
			}(),
			ExpectedScheduledIndices:               []int{1},
			ExpectedCumulativeScheduledJobs:        []int{0, 1},
			ExpectedRuntimeGangCardinality:         []int{0, 1},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{0},
		},
		"Home-away scheduling": {
			SchedulingConfig: func() configuration.SchedulingConfig {
				config := testfixtures.TestSchedulingConfig()
				config.PriorityClasses = map[string]types.PriorityClass{
					"armada-preemptible": {
						Priority:    30000,
						Preemptible: true,
					},
					"armada-preemptible-away": {
						Priority:    30000,
						Preemptible: true,
						AwayNodeTypes: []types.AwayNodeType{
							{Priority: 29000, WellKnownNodeTypeName: "node-type-a"},
						},
					},
				}
				config.DefaultPriorityClassName = "armada-preemptible"
				config.WellKnownNodeTypes = []configuration.WellKnownNodeType{
					{
						Name: "node-type-a",
						Taints: []v1.Taint{
							{Key: "taint-a", Value: "true", Effect: v1.TaintEffectNoSchedule},
						},
					},
				}
				return config
			}(),
			Nodes: testfixtures.TestNodeFactory.AddTaints(
				testfixtures.N32CpuNodes(1, []int32{29000, 30000}),
				[]v1.Taint{
					{Key: "taint-a", Value: "true", Effect: v1.TaintEffectNoSchedule},
				},
			),
			Gangs: func() (gangs [][]*jobdb.Job) {
				jobId := util.ULID()
				gangs = append(gangs, []*jobdb.Job{testfixtures.TestJob("A", jobId, "armada-preemptible-away", testfixtures.Test32Cpu256GiWithLargeJobTolerationPodReqs("A", jobId, 30000))})
				jobId = util.ULID()
				gangs = append(gangs, []*jobdb.Job{testfixtures.TestJob("A", jobId, "armada-preemptible-away", testfixtures.Test32Cpu256GiWithLargeJobTolerationPodReqs("A", jobId, 30000))})
				return
			}(),
			ExpectedScheduledIndices:               []int{0},
			ExpectedCumulativeScheduledJobs:        []int{1, 1},
			ExpectedRuntimeGangCardinality:         []int{1, 0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{1},
		},
		"Hitting queue constraint does not make job scheduling key unfeasible": {
			SchedulingConfig: func() configuration.SchedulingConfig {
				cfg := testfixtures.TestSchedulingConfig()
				cfg.MaximumPerQueueSchedulingBurst = 2
				cfg.MaximumPerQueueSchedulingRate = 2
				return cfg
			}(),
			Nodes: testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 2)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1Cpu4GiJobs("B", testfixtures.PriorityClass0, 1)),
			},
			ExpectedScheduledIndices:               []int{0, 2},
			ExpectedCumulativeScheduledJobs:        []int{2, 2, 3},
			ExpectedRuntimeGangCardinality:         []int{2, 0, 1},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{},
		},
		"Hitting globally applicable constraint makes job scheduling key unfeasible": {
			SchedulingConfig: testfixtures.TestSchedulingConfig(),
			Nodes:            testfixtures.N32CpuNodes(1, testfixtures.TestPriorities),
			Gangs: [][]*jobdb.Job{
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1GpuJobs("A", testfixtures.PriorityClass0, 1)),
				testfixtures.WithGangAnnotationsJobs(testfixtures.N1GpuJobs("B", testfixtures.PriorityClass0, 1)),
			},
			ExpectedCumulativeScheduledJobs:        []int{0, 0, 0},
			ExpectedRuntimeGangCardinality:         []int{0, 0, 0},
			ExpectedUnfeasibleSchedulingKeyIndices: []int{0},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// This is hacktabulous. Essentially the jobs have the wrong priority classes set at this point
			// because textfixtures.TestJob() initialises the jobs using a jobDb that doesn't know anything about the
			// priority classes we have defined in this test.  We therefore need to fix the priority classes here.
			// The long term strategy is to try and remove the need to have a jobDB for creating jobs.
			for i, gang := range tc.Gangs {
				for j, job := range gang {
					tc.Gangs[i][j] = job.WithPriorityClass(tc.SchedulingConfig.PriorityClasses[job.PriorityClassName()])
				}
			}

			expectedUnfeasibleJobSchedulingKeys := make([]internaltypes.SchedulingKey, 0)
			// We need to populate expectedUnfeasibleJobSchedulingKeys from the jobSchedulingContexts of the relevant
			// gangs, because it's hard to do within the testcase
			for _, unfeasibleGangIdx := range tc.ExpectedUnfeasibleSchedulingKeyIndices {
				gang := tc.Gangs[unfeasibleGangIdx]
				jctxs := context.JobSchedulingContextsFromJobs(gang)
				require.Equal(t, 1, len(jctxs), fmt.Sprintf("gangs with cardinality greater than 1 don't have a single scheduling key: %v", gang))
				jctx := jctxs[0]
				key, _ := jctx.SchedulingKey()
				require.NotEqual(t, key, internaltypes.EmptySchedulingKey, "expected unfeasible scheduling key cannot be the empty key")

				expectedUnfeasibleJobSchedulingKeys = append(expectedUnfeasibleJobSchedulingKeys, key)
			}

			nodesById := make(map[string]*internaltypes.Node, len(tc.Nodes))
			for _, node := range tc.Nodes {
				nodesById[node.GetId()] = node
			}
			nodeDb, err := nodedb.NewNodeDb(
				tc.SchedulingConfig.PriorityClasses,
				tc.SchedulingConfig.IndexedResources,
				tc.SchedulingConfig.IndexedTaints,
				tc.SchedulingConfig.IndexedNodeLabels,
				tc.SchedulingConfig.WellKnownNodeTypes,
				testfixtures.TestResourceListFactory,
			)
			require.NoError(t, err)
			txn := nodeDb.Txn(true)
			for _, node := range tc.Nodes {
				err = nodeDb.CreateAndInsertWithJobDbJobsWithTxn(txn, nil, node.DeepCopyNilKeys())
				require.NoError(t, err)
			}
			txn.Commit()
			if tc.TotalResources.AllZero() {
				// Default to NodeDb total.
				tc.TotalResources = nodeDb.TotalKubernetesResources()
			}
			priorityFactorByQueue := make(map[string]float64)
			for _, jobs := range tc.Gangs {
				for _, job := range jobs {
					priorityFactorByQueue[job.Queue()] = 1
				}
			}

			fairnessCostProvider, err := fairness.NewDominantResourceFairness(
				tc.TotalResources,
				testfixtures.TestPool,
				tc.SchedulingConfig,
			)
			require.NoError(t, err)
			sctx := context.NewSchedulingContext(
				"pool",
				fairnessCostProvider,
				rate.NewLimiter(
					rate.Limit(tc.SchedulingConfig.MaximumSchedulingRate),
					tc.SchedulingConfig.MaximumSchedulingBurst,
				),
				tc.TotalResources,
			)
			for queue, priorityFactor := range priorityFactorByQueue {
				err := sctx.AddQueueSchedulingContext(
					queue,
					priorityFactor,
					priorityFactor,
					nil,
					internaltypes.ResourceList{},
					internaltypes.ResourceList{},
					internaltypes.ResourceList{},
					rate.NewLimiter(
						rate.Limit(tc.SchedulingConfig.MaximumPerQueueSchedulingRate),
						tc.SchedulingConfig.MaximumPerQueueSchedulingBurst,
					),
				)
				require.NoError(t, err)
			}
			constraints := schedulerconstraints.NewSchedulingConstraints(
				"pool",
				tc.TotalResources,
				tc.SchedulingConfig,
				armadaslices.Map(
					maps.Keys(priorityFactorByQueue),
					func(qn string) *api.Queue { return &api.Queue{Name: qn} },
				),
			)
			floatingResourceTypes, err := floatingresources.NewFloatingResourceTypes(tc.SchedulingConfig.FloatingResources, testfixtures.TestResourceListFactory)
			require.NoError(t, err)
			sch, err := NewGangScheduler(sctx, constraints, floatingResourceTypes, nodeDb, false)
			require.NoError(t, err)

			var actualScheduledIndices []int
			scheduledGangs := 0
			for i, gang := range tc.Gangs {
				jctxs := context.JobSchedulingContextsFromJobs(gang)
				gctx := context.NewGangSchedulingContext(jctxs)
				ok, reason, err := sch.Schedule(armadacontext.Background(), gctx)
				require.NoError(t, err)

				// Runtime cardinality should match expected scheduled jobs
				require.Equal(t, tc.ExpectedRuntimeGangCardinality[i], gctx.Fit().NumScheduled)

				if ok {
					require.Empty(t, reason)
					actualScheduledIndices = append(actualScheduledIndices, i)

					// If there's a node uniformity constraint, check that it's met.
					if nodeUniformity := gctx.GangInfo.NodeUniformity; nodeUniformity != "" {
						nodeUniformityLabelValues := make(map[string]bool)
						for _, jctx := range jctxs {
							pctx := jctx.PodSchedulingContext
							if !pctx.IsSuccessful() {
								continue
							}
							node := nodesById[pctx.NodeId]
							require.NotNil(t, node)
							value, ok := node.GetLabelValue(nodeUniformity)
							require.True(t, ok, "gang job scheduled onto node with missing nodeUniformityLabel")
							nodeUniformityLabelValues[value] = true
						}
						require.Equal(
							t, 1, len(nodeUniformityLabelValues),
							"node uniformity constraint not met: %s", nodeUniformityLabelValues,
						)
						if expectedValue, ok := tc.ExpectedNodeUniformity[i]; ok {
							actualValue := maps.Keys(nodeUniformityLabelValues)[0]
							require.Equal(t, expectedValue, actualValue)
						}
					}

					// Verify accounting
					scheduledGangs++
					require.Equal(t, scheduledGangs, sch.schedulingContext.NumScheduledGangs)
					require.Equal(t, tc.ExpectedCumulativeScheduledJobs[i], sch.schedulingContext.NumScheduledJobs)
					require.Equal(t, 0, sch.schedulingContext.NumEvictedJobs)
				} else {
					require.NotEmpty(t, reason)

					// Verify all jobs have been correctly unbound from nodes
					for _, jctx := range jctxs {
						if jctx.PodSchedulingContext != nil {
							require.Equal(t, "", jctx.PodSchedulingContext.NodeId)
						}
					}

					// Verify accounting
					require.Equal(t, scheduledGangs, sch.schedulingContext.NumScheduledGangs)
					require.Equal(t, tc.ExpectedCumulativeScheduledJobs[i], sch.schedulingContext.NumScheduledJobs)
					require.Equal(t, 0, sch.schedulingContext.NumEvictedJobs)
				}
			}
			assert.Equal(t, tc.ExpectedScheduledIndices, actualScheduledIndices)
			assert.Equal(t, expectedUnfeasibleJobSchedulingKeys, maps.Keys(sch.schedulingContext.UnfeasibleSchedulingKeys))
		})
	}
}

func addFloatingResourceRequest(request string, jobs []*jobdb.Job) []*jobdb.Job {
	return testfixtures.WithRequestsJobs(
		schedulerobjects.ResourceList{
			Resources: map[string]*resource.Quantity{
				"test-floating-resource": pointer.MustParseResource(request),
			},
		},
		jobs)
}
