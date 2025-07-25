package scheduler

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gogo/protobuf/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
	v1 "k8s.io/api/core/v1"
	clock "k8s.io/utils/clock/testing"
	"k8s.io/utils/pointer"

	"github.com/armadaproject/armada/internal/common/armadacontext"
	"github.com/armadaproject/armada/internal/common/ingest/utils"
	protoutil "github.com/armadaproject/armada/internal/common/proto"
	"github.com/armadaproject/armada/internal/common/pulsarutils"
	"github.com/armadaproject/armada/internal/common/util"
	"github.com/armadaproject/armada/internal/scheduler/database"
	schedulerdb "github.com/armadaproject/armada/internal/scheduler/database"
	"github.com/armadaproject/armada/internal/scheduler/internaltypes"
	"github.com/armadaproject/armada/internal/scheduler/jobdb"
	"github.com/armadaproject/armada/internal/scheduler/kubernetesobjects/affinity"
	"github.com/armadaproject/armada/internal/scheduler/leader"
	"github.com/armadaproject/armada/internal/scheduler/metrics"
	"github.com/armadaproject/armada/internal/scheduler/pricing"
	"github.com/armadaproject/armada/internal/scheduler/schedulerobjects"
	"github.com/armadaproject/armada/internal/scheduler/scheduling"
	schedulercontext "github.com/armadaproject/armada/internal/scheduler/scheduling/context"
	"github.com/armadaproject/armada/internal/scheduler/testfixtures"
	"github.com/armadaproject/armada/internal/scheduleringester"
	apiconfig "github.com/armadaproject/armada/internal/server/configuration"
	"github.com/armadaproject/armada/pkg/api"
	"github.com/armadaproject/armada/pkg/armadaevents"
	"github.com/armadaproject/armada/pkg/bidstore"
	"github.com/armadaproject/armada/pkg/metricevents"
)

// Data to be used in tests
const (
	maxNumberOfAttempts = 2
	nodeIdLabel         = "kubernetes.io/hostname"
)

var (
	failFastSchedulingInfo = &schedulerobjects.JobSchedulingInfo{
		AtMostOnce:        true,
		PriorityClassName: testfixtures.PriorityClass2NonPreemptible,
		ObjectRequirements: []*schedulerobjects.ObjectRequirements{
			{
				Requirements: &schedulerobjects.ObjectRequirements_PodRequirements{
					PodRequirements: &schedulerobjects.PodRequirements{
						Annotations: map[string]string{
							apiconfig.FailFastAnnotation: "true",
						},
					},
				},
			},
		},
		Version: 1,
	}
	failFastSchedulingInfoBytes = protoutil.MustMarshall(failFastSchedulingInfo)
	preemptibleSchedulingInfo   = &schedulerobjects.JobSchedulingInfo{
		AtMostOnce:        true,
		Preemptible:       true,
		PriorityClassName: testfixtures.PriorityClass1,
		ObjectRequirements: []*schedulerobjects.ObjectRequirements{
			{
				Requirements: &schedulerobjects.ObjectRequirements_PodRequirements{
					PodRequirements: &schedulerobjects.PodRequirements{},
				},
			},
		},
		Version: 1,
	}
	preemptibleSchedulingInfoBytes = protoutil.MustMarshall(preemptibleSchedulingInfo)
	preemptibleGangSchedulingInfo  = &schedulerobjects.JobSchedulingInfo{
		AtMostOnce:        true,
		Preemptible:       true,
		PriorityClassName: testfixtures.PriorityClass1,
		ObjectRequirements: []*schedulerobjects.ObjectRequirements{
			{
				Requirements: &schedulerobjects.ObjectRequirements_PodRequirements{
					PodRequirements: &schedulerobjects.PodRequirements{
						Annotations: map[string]string{
							apiconfig.GangCardinalityAnnotation:         "4",
							apiconfig.GangIdAnnotation:                  "id",
							apiconfig.GangNodeUniformityLabelAnnotation: "uniformity",
						},
					},
				},
			},
		},
		Version: 1,
	}
	schedulingInfo = &schedulerobjects.JobSchedulingInfo{
		AtMostOnce:        true,
		PriorityClassName: testfixtures.PriorityClass2NonPreemptible,
		ObjectRequirements: []*schedulerobjects.ObjectRequirements{
			{
				Requirements: &schedulerobjects.ObjectRequirements_PodRequirements{
					PodRequirements: &schedulerobjects.PodRequirements{},
				},
			},
		},
		Version: 1,
	}
	schedulingInfoBytes   = protoutil.MustMarshall(schedulingInfo)
	updatedSchedulingInfo = &schedulerobjects.JobSchedulingInfo{
		AtMostOnce: true,
		ObjectRequirements: []*schedulerobjects.ObjectRequirements{
			{
				Requirements: &schedulerobjects.ObjectRequirements_PodRequirements{
					PodRequirements: &schedulerobjects.PodRequirements{},
				},
			},
		},
		Version: 2,
	}
	updatedSchedulingInfoBytes        = protoutil.MustMarshall(updatedSchedulingInfo)
	schedulingInfoWithUpdatedPriority = &schedulerobjects.JobSchedulingInfo{
		AtMostOnce:        true,
		PriorityClassName: testfixtures.PriorityClass2NonPreemptible,
		ObjectRequirements: []*schedulerobjects.ObjectRequirements{
			{
				Requirements: &schedulerobjects.ObjectRequirements_PodRequirements{
					PodRequirements: &schedulerobjects.PodRequirements{},
				},
			},
		},
		Version: 2,
	}
	schedulingInfoWithUpdatedPriorityBytes = protoutil.MustMarshall(schedulingInfoWithUpdatedPriority)

	schedulerMetrics, _ = metrics.New(nil, nil, []time.Duration{}, 12*time.Hour, pulsarutils.NoOpPublisher[*metricevents.Event]{})
)

var queuedJob = testfixtures.NewJob(
	util.NewULID(),
	"testJobset",
	"testQueue",
	uint32(10),
	toInternalSchedulingInfo(schedulingInfo),
	true,
	0,
	false,
	false,
	false,
	1,
	true,
)

var leasedJob = testfixtures.NewJob(
	util.NewULID(),
	"testJobset",
	"testQueue",
	0,
	toInternalSchedulingInfo(schedulingInfo),
	false,
	1,
	false,
	false,
	false,
	1,
	true,
).WithNewRun("testExecutor", "test-node", "node", "pool", 5)

var preemptibleLeasedJob = testfixtures.NewJob(
	util.NewULID(),
	"testJobset",
	"testQueue",
	0,
	toInternalSchedulingInfo(preemptibleSchedulingInfo),
	false,
	1,
	false,
	false,
	false,
	1,
	true,
).WithNewRun("testExecutor", "test-node", "node", "pool", 5)

var cancelledJob = testfixtures.NewJob(
	util.NewULID(),
	"testJobset",
	"testQueue",
	0,
	toInternalSchedulingInfo(schedulingInfo),
	false,
	1,
	true,
	false,
	true,
	1,
	true,
).WithNewRun("testExecutor", "test-node", "node", "pool", 5)

var returnedOnceLeasedJob = testfixtures.NewJob(
	"01h3w2wtdchtc80hgyp782shrv",
	"testJobset",
	"testQueue",
	uint32(10),
	toInternalSchedulingInfo(schedulingInfo),
	false,
	3,
	false,
	false,
	false,
	1,
	true,
).WithUpdatedRun(testfixtures.JobDb.CreateRun(
	uuid.NewString(),
	"01h3w2wtdchtc80hgyp782shrv",
	0,
	"testExecutor",
	"testNodeId",
	"testNodeName",
	"pool",
	&scheduledAtPriority,
	false,
	false,
	false,
	false,
	false,
	false,
	true,
	false,
	nil,
	nil,
	nil,
	nil,
	nil,
	true,
	true,
)).WithNewRun("testExecutor", "test-node", "node", "pool", 5)

var defaultJobError = &armadaevents.Error{
	Terminal: true,
	Reason: &armadaevents.Error_PodError{
		PodError: &armadaevents.PodError{
			Message: "generic pod error",
		},
	},
}

func defaultJobRunError(jobId string, runId string) *armadaevents.JobRunErrors {
	return &armadaevents.JobRunErrors{
		JobId: jobId,
		RunId: runId,
		Errors: []*armadaevents.Error{
			defaultJobError,
		},
	}
}

var leasedFailFastJob = testfixtures.NewJob(
	util.NewULID(),
	"testJobset",
	"testQueue",
	uint32(10),
	toInternalSchedulingInfo(failFastSchedulingInfo),
	false,
	1,
	false,
	false,
	false,
	1,
	true,
).WithNewRun("testExecutor", "test-node", "node", "pool", 5)

var (
	preemptibleGangJob1 = createPreemptibleGangJob()
	preemptibleGangJob2 = createPreemptibleGangJob()
)

var (
	testExecutor        = "test-executor"
	testNode            = "test-node"
	testPool            = "test-pool"
	testNodeId          = api.NodeIdFromExecutorAndNodeName(testExecutor, testNode)
	scheduledAtPriority = int32(2)
	requeuedJobId       = util.NewULID()
	requeuedJob         = testfixtures.NewJob(
		requeuedJobId,
		"testJobset",
		"testQueue",
		uint32(10),
		toInternalSchedulingInfo(schedulingInfo),
		true,
		2,
		false,
		false,
		false,
		1,
		true,
	).WithUpdatedRun(testfixtures.JobDb.CreateRun(
		uuid.NewString(),
		requeuedJobId,
		time.Now().Unix(),
		"testExecutor",
		"test-node",
		"node",
		"pool",
		&scheduledAtPriority,
		false,
		false,
		false,
		false,
		false,
		false,
		true,
		false,
		nil,
		nil,
		nil,
		nil,
		nil,
		true,
		true,
	))
)

// Test a single scheduler cycle
func TestScheduler_TestCycle(t *testing.T) {
	tests := map[string]struct {
		initialJobs                      []*jobdb.Job                   // jobs in the jobDb at the start of the cycle
		jobUpdates                       []database.Job                 // job updates from the database
		runUpdates                       []database.Run                 // run updates from the database
		jobRunErrors                     map[string]*armadaevents.Error // job run errors in the database
		staleExecutor                    bool                           // if true then the executorRepository will report the executor as stale
		fetchError                       bool                           // if true then the jobRepository will throw an error
		scheduleError                    bool                           // if true then the scheduling algo will throw an error
		publishError                     bool                           // if true the publisher will throw an error
		submitCheckerFailure             bool                           // if true the submit checker will say the job is unschedulable
		expectedJobRunLeased             []string                       // ids of jobs we expect to have produced leased messages
		expectedJobRunErrors             []string                       // ids of jobs we expect to have produced jobRunErrors messages
		expectedJobErrors                []string                       // ids of jobs we expect to have produced jobErrors messages
		expectedJobsRunsToPreempt        []string                       // ids of jobs we expect to be preempted by the scheduler
		expectedJobRunPreempted          []string                       // ids of jobs we expect to have produced jobRunPreempted messages
		expectedJobRunCancelled          []string                       // ids of jobs we expect to have produced jobRunPreempted messages
		expectedJobCancelled             []string                       // ids of jobs we expect to have  produced cancelled messages
		expectedJobRequestCancel         []string                       // ids of jobs we expect to have produced request cancel
		expectedJobReprioritised         []string                       // ids of jobs we expect to have  produced reprioritised messages
		expectedQueued                   []string                       // ids of jobs we expect to have  produced requeued messages
		expectedJobSucceeded             []string                       // ids of jobs we expect to have  produced succeeeded messages
		expectedLeased                   []string                       // ids of jobs we expected to be leased in jobdb at the end of the cycle
		expectedRequeued                 []string                       // ids of jobs we expected to be requeued in jobdb at the end of the cycle
		expectedValidated                []string                       // ids of jobs we expected to have produced submit checked messages
		expectedTerminal                 []string                       // ids of jobs we expected to be terminal in jobdb at the end of the cycle
		expectedJobPriority              map[string]uint32              // expected priority of jobs at the end of the cycle
		expectedNodeAntiAffinities       []string                       // list of nodes there is expected to be anti affinities for on job scheduling info
		expectedJobSchedulingInfoVersion int                            // expected scheduling info version of jobs at the end of the cycle
		expectedQueuedVersion            int32                          // expected queued version of jobs at the end of the cycle
	}{
		"Lease a single job already in the db": {
			initialJobs:           []*jobdb.Job{queuedJob},
			expectedJobRunLeased:  []string{queuedJob.Id()},
			expectedLeased:        []string{queuedJob.Id()},
			expectedQueuedVersion: 1,
		},
		"Submit check a job": {
			initialJobs:           []*jobdb.Job{queuedJob.WithValidated(false)},
			expectedQueued:        []string{queuedJob.Id()},
			expectedValidated:     []string{queuedJob.Id()},
			expectedQueuedVersion: 0,
		},
		"Lease a single job from an update": {
			jobUpdates: []database.Job{
				{
					JobID:                 queuedJob.Id(),
					JobSet:                "testJobSet",
					Queue:                 "testQueue",
					Queued:                true,
					QueuedVersion:         0,
					SchedulingInfo:        schedulingInfoBytes,
					SchedulingInfoVersion: int32(schedulingInfo.Version),
					Serial:                1,
					Validated:             true,
				},
			},
			expectedJobRunLeased:  []string{queuedJob.Id()},
			expectedLeased:        []string{queuedJob.Id()},
			expectedQueuedVersion: 1,
		},
		"Lease two jobs from an update": {
			jobUpdates: []database.Job{
				{
					JobID:                 "01h3w2wtdchtc80hgyp782shrv",
					JobSet:                "testJobSet",
					Queue:                 "testQueue",
					Queued:                true,
					QueuedVersion:         0,
					SchedulingInfo:        schedulingInfoBytes,
					SchedulingInfoVersion: int32(schedulingInfo.Version),
					Serial:                1,
					Validated:             true,
				},
				{
					JobID:                 "01h434g4hxww2pknb2q1nfmfph",
					JobSet:                "testJobSet",
					Queue:                 "testQueue",
					Queued:                true,
					QueuedVersion:         0,
					SchedulingInfo:        schedulingInfoBytes,
					SchedulingInfoVersion: int32(schedulingInfo.Version),
					Serial:                1,
					Validated:             true,
				},
			},
			expectedJobRunLeased:  []string{"01h3w2wtdchtc80hgyp782shrv", "01h434g4hxww2pknb2q1nfmfph"},
			expectedLeased:        []string{"01h3w2wtdchtc80hgyp782shrv", "01h434g4hxww2pknb2q1nfmfph"},
			expectedQueuedVersion: 1,
		},
		"New failed job with no runs": {
			// This happens if the scheduler decides to fail a job, e.g., due to min-max gang scheduling.
			jobUpdates: []database.Job{
				{
					JobID:                 queuedJob.Id(),
					JobSet:                "testJobSet",
					Queue:                 "testQueue",
					Failed:                true,
					QueuedVersion:         0,
					SchedulingInfo:        schedulingInfoBytes,
					SchedulingInfoVersion: int32(schedulingInfo.Version),
					Serial:                1,
				},
			},
			expectedQueuedVersion: 0,
		},
		"Queued job transitions straight to failed without running": {
			// This happens if the scheduler decides to fail a job, e.g., due to min-max gang scheduling.
			initialJobs: []*jobdb.Job{queuedJob},
			jobUpdates: []database.Job{
				{
					JobID:                 queuedJob.Id(),
					JobSet:                "testJobSet",
					Queue:                 "testQueue",
					Failed:                true,
					QueuedVersion:         0,
					SchedulingInfo:        schedulingInfoBytes,
					SchedulingInfoVersion: int32(schedulingInfo.Version),
					Serial:                1,
				},
			},
			expectedQueuedVersion: 0,
		},
		"Nothing leased": {
			initialJobs:           []*jobdb.Job{queuedJob},
			expectedQueued:        []string{queuedJob.Id()},
			expectedQueuedVersion: queuedJob.QueuedVersion(),
		},
		"No updates to an already leased job": {
			initialJobs:           []*jobdb.Job{leasedJob},
			expectedLeased:        []string{leasedJob.Id()},
			expectedQueuedVersion: leasedJob.QueuedVersion(),
		},
		"No updates to a requeued job already db": {
			initialJobs:           []*jobdb.Job{requeuedJob},
			expectedQueued:        []string{requeuedJob.Id()},
			expectedQueuedVersion: requeuedJob.QueuedVersion(),
		},
		"No updates to a requeued job from update": {
			jobUpdates: []database.Job{
				{
					JobID:                 requeuedJob.Id(),
					JobSet:                "testJobSet",
					Queue:                 "testQueue",
					Queued:                true,
					QueuedVersion:         2,
					SchedulingInfo:        schedulingInfoBytes,
					SchedulingInfoVersion: int32(schedulingInfo.Version),
					Serial:                1,
					Validated:             true,
				},
			},
			runUpdates: []database.Run{
				{
					RunID:        requeuedJob.LatestRun().Id(),
					JobID:        requeuedJob.Id(),
					JobSet:       "testJobSet",
					Executor:     "testExecutor",
					Node:         "node",
					Failed:       true,
					Returned:     true,
					RunAttempted: true,
					Serial:       1,
				},
			},
			expectedQueued:        []string{requeuedJob.Id()},
			expectedQueuedVersion: requeuedJob.QueuedVersion(),
		},
		"Lease returned and re-queued when run attempted": {
			initialJobs: []*jobdb.Job{leasedJob},
			runUpdates: []database.Run{
				{
					RunID:        leasedJob.LatestRun().Id(),
					JobID:        leasedJob.Id(),
					JobSet:       "testJobSet",
					Executor:     "testExecutor",
					Failed:       true,
					Returned:     true,
					RunAttempted: true,
					Serial:       1,
				},
			},
			expectedQueued:   []string{leasedJob.Id()},
			expectedRequeued: []string{leasedJob.Id()},
			// Should add node anti affinities for nodes of any attempted runs
			expectedNodeAntiAffinities:       []string{leasedJob.LatestRun().NodeName()},
			expectedJobSchedulingInfoVersion: 2,
			expectedQueuedVersion:            leasedJob.QueuedVersion() + 1,
		},
		"Lease returned and re-queued when run not attempted": {
			initialJobs: []*jobdb.Job{leasedJob},
			runUpdates: []database.Run{
				{
					RunID:        leasedJob.LatestRun().Id(),
					JobID:        leasedJob.Id(),
					JobSet:       "testJobSet",
					Executor:     "testExecutor",
					Failed:       true,
					Returned:     true,
					RunAttempted: false,
					Serial:       1,
				},
			},
			expectedQueued:        []string{leasedJob.Id()},
			expectedRequeued:      []string{leasedJob.Id()},
			expectedQueuedVersion: leasedJob.QueuedVersion() + 1,
		},
		// When a lease is returned and the run was attempted, a node anti affinity is added
		// If this node anti-affinity makes the job unschedulable, it should be failed.
		"Lease returned and failed": {
			initialJobs: []*jobdb.Job{leasedJob.WithValidated(true)},
			runUpdates: []database.Run{
				{
					RunID:        leasedJob.LatestRun().Id(),
					JobID:        leasedJob.Id(),
					JobSet:       "testJobSet",
					Executor:     "testExecutor",
					Failed:       true,
					Returned:     true,
					RunAttempted: true,
					Serial:       1,
				},
			},
			jobRunErrors: map[string]*armadaevents.Error{
				leasedJob.LatestRun().Id(): defaultJobError,
			},
			submitCheckerFailure:  true,
			expectedJobErrors:     []string{leasedJob.Id()},
			expectedTerminal:      []string{leasedJob.Id()},
			expectedQueuedVersion: leasedJob.QueuedVersion(),
		},
		"Lease returned too many times": {
			initialJobs: []*jobdb.Job{returnedOnceLeasedJob},
			runUpdates: []database.Run{
				{
					RunID:        returnedOnceLeasedJob.LatestRun().Id(),
					JobID:        returnedOnceLeasedJob.Id(),
					JobSet:       "testJobSet",
					Executor:     "testExecutor",
					Node:         "testNode",
					Failed:       true,
					Returned:     true,
					RunAttempted: true,
					Serial:       2,
				},
			},
			jobRunErrors: map[string]*armadaevents.Error{
				returnedOnceLeasedJob.LatestRun().Id(): defaultJobError,
			},
			expectedJobErrors:     []string{returnedOnceLeasedJob.Id()},
			expectedTerminal:      []string{returnedOnceLeasedJob.Id()},
			expectedQueuedVersion: 3,
		},
		"Lease returned for fail fast job": {
			initialJobs: []*jobdb.Job{leasedFailFastJob},
			// Fail fast should mean there is only ever 1 attempted run
			runUpdates: []database.Run{
				{
					RunID:        leasedFailFastJob.LatestRun().Id(),
					JobID:        leasedFailFastJob.Id(),
					JobSet:       "testJobSet",
					Executor:     "testExecutor",
					Failed:       true,
					Returned:     true,
					RunAttempted: false,
					Serial:       1,
				},
			},
			jobRunErrors: map[string]*armadaevents.Error{
				leasedFailFastJob.LatestRun().Id(): defaultJobError,
			},
			expectedJobErrors:     []string{leasedFailFastJob.Id()},
			expectedTerminal:      []string{leasedFailFastJob.Id()},
			expectedQueuedVersion: leasedFailFastJob.QueuedVersion(),
		},
		"Job cancelled": {
			initialJobs: []*jobdb.Job{leasedJob},
			jobUpdates: []database.Job{
				{
					JobID:           leasedJob.Id(),
					JobSet:          "testJobSet",
					Queue:           "testQueue",
					CancelRequested: true,
					Serial:          1,
				},
			},
			expectedJobRunCancelled: []string{leasedJob.Id()},
			expectedJobCancelled:    []string{leasedJob.Id()},
			expectedTerminal:        []string{leasedJob.Id()},
			expectedQueuedVersion:   leasedJob.QueuedVersion(),
		},
		"Job Run preemption requested": {
			initialJobs: []*jobdb.Job{preemptibleLeasedJob},
			runUpdates: []database.Run{
				{
					RunID:            preemptibleLeasedJob.LatestRun().Id(),
					JobID:            preemptibleLeasedJob.Id(),
					JobSet:           "testJobSet",
					Executor:         "testExecutor",
					PreemptRequested: true,
					Serial:           1,
				},
			},
			expectedJobRunPreempted: []string{preemptibleLeasedJob.Id()},
			expectedJobErrors:       []string{preemptibleLeasedJob.Id()},
			expectedJobRunErrors:    []string{preemptibleLeasedJob.Id()},
			expectedTerminal:        []string{preemptibleLeasedJob.Id()},
			expectedQueuedVersion:   preemptibleLeasedJob.QueuedVersion(),
		},
		"Gang Job Run preemption requested - preempts all members of the same gang": {
			initialJobs: []*jobdb.Job{preemptibleGangJob1, preemptibleGangJob2},
			runUpdates: []database.Run{
				{
					RunID:            preemptibleGangJob1.LatestRun().Id(),
					JobID:            preemptibleGangJob1.Id(),
					JobSet:           "testJobSet",
					Executor:         "testExecutor",
					PreemptRequested: true,
					Serial:           1,
				},
			},
			expectedJobRunPreempted: []string{preemptibleGangJob1.Id(), preemptibleGangJob2.Id()},
			expectedJobErrors:       []string{preemptibleGangJob1.Id(), preemptibleGangJob2.Id()},
			expectedJobRunErrors:    []string{preemptibleGangJob1.Id(), preemptibleGangJob2.Id()},
			expectedTerminal:        []string{preemptibleGangJob1.Id(), preemptibleGangJob2.Id()},
			expectedQueuedVersion:   preemptibleGangJob1.QueuedVersion(),
		},
		"Job Run preemption requested - job not pre-emptible - no action expected": {
			initialJobs: []*jobdb.Job{leasedJob},
			runUpdates: []database.Run{
				{
					RunID:            leasedJob.LatestRun().Id(),
					JobID:            leasedJob.Id(),
					JobSet:           "testJobSet",
					Executor:         "testExecutor",
					PreemptRequested: true,
					Serial:           1,
				},
			},
			expectedLeased:        []string{leasedJob.Id()},
			expectedQueuedVersion: leasedJob.QueuedVersion(),
		},
		"New postgres job with cancel requested results in cancel messages": {
			jobUpdates: []database.Job{
				{
					JobID:           queuedJob.Id(),
					JobSet:          queuedJob.Jobset(),
					Queue:           queuedJob.Queue(),
					Queued:          queuedJob.Queued(),
					QueuedVersion:   queuedJob.QueuedVersion(),
					Serial:          1,
					Submitted:       queuedJob.Created(),
					CancelRequested: true,
					Cancelled:       false,
					SchedulingInfo:  schedulingInfoBytes,
				},
			},

			// We have already got a request cancel from the DB, so only publish a cancelled message.
			// The job should also be removed from the queue and set to a terminal state.#
			expectedJobCancelled:  []string{queuedJob.Id()},
			expectedQueuedVersion: queuedJob.QueuedVersion(),
			expectedTerminal:      []string{queuedJob.Id()},
		},
		"Postgres job with cancel requested results in cancel messages": {
			initialJobs: []*jobdb.Job{queuedJob.WithCancelRequested(true)},
			jobUpdates: []database.Job{
				{
					JobID:           queuedJob.Id(),
					JobSet:          queuedJob.Jobset(),
					Queue:           queuedJob.Queue(),
					Queued:          queuedJob.Queued(),
					QueuedVersion:   queuedJob.QueuedVersion(),
					Serial:          1,
					Submitted:       queuedJob.Created(),
					CancelRequested: true,
					Cancelled:       false,
					SchedulingInfo:  schedulingInfoBytes,
					Priority:        int64(queuedJob.Priority()),
				},
			},

			// We have already got a request cancel from the DB/existing job state, so only publish a cancelled message.
			// The job should also be removed from the queue and set to a terminal state.
			expectedJobCancelled:  []string{queuedJob.Id()},
			expectedQueuedVersion: queuedJob.QueuedVersion(),
			expectedTerminal:      []string{queuedJob.Id()},
		},
		"Queued job reprioritised": {
			initialJobs: []*jobdb.Job{queuedJob},
			jobUpdates: []database.Job{
				{
					JobID:    queuedJob.Id(),
					JobSet:   "testJobSet",
					Queue:    "testQueue",
					Priority: 2,
					Serial:   1,
				},
			},
			expectedJobReprioritised: []string{queuedJob.Id()},
			expectedQueued:           []string{queuedJob.Id()},
			expectedJobPriority:      map[string]uint32{queuedJob.Id(): 2},
			expectedQueuedVersion:    queuedJob.QueuedVersion(),
		},
		"Leased job reprioritised": {
			initialJobs: []*jobdb.Job{leasedJob},
			jobUpdates: []database.Job{
				{
					JobID:    leasedJob.Id(),
					JobSet:   "testJobSet",
					Queue:    "testQueue",
					Priority: 2,
					Serial:   1,
				},
			},
			expectedJobReprioritised: []string{leasedJob.Id()},
			expectedLeased:           []string{leasedJob.Id()},
			expectedJobPriority:      map[string]uint32{leasedJob.Id(): 2},
			expectedQueuedVersion:    leasedJob.QueuedVersion(),
		},
		"Terminal job reprioritised": {
			initialJobs: []*jobdb.Job{cancelledJob},
			jobUpdates: []database.Job{
				{
					JobID:    cancelledJob.Id(),
					JobSet:   "testJobSet",
					Queue:    "testQueue",
					Priority: 2,
					Serial:   1,
				},
			},
			// No events should be sent for a terminal job when it is reprioritised
			expectedJobReprioritised: []string{},
			expectedLeased:           []string{},
			expectedJobPriority:      map[string]uint32{cancelledJob.Id(): cancelledJob.Priority()},
			expectedQueuedVersion:    cancelledJob.QueuedVersion(),
		},
		"Lease expired": {
			initialJobs:           []*jobdb.Job{leasedJob},
			staleExecutor:         true,
			expectedJobRunErrors:  []string{leasedJob.Id()},
			expectedJobErrors:     []string{leasedJob.Id()},
			expectedTerminal:      []string{leasedJob.Id()},
			expectedQueuedVersion: leasedJob.QueuedVersion(),
		},
		"Submit check failed": {
			initialJobs:          []*jobdb.Job{queuedJob.WithValidated(false)},
			submitCheckerFailure: true,
			expectedJobErrors:    []string{queuedJob.Id()},
			expectedTerminal:     []string{queuedJob.Id()},
		},
		"Job failed": {
			initialJobs: []*jobdb.Job{leasedJob},
			runUpdates: []database.Run{
				{
					RunID:    leasedJob.LatestRun().Id(),
					JobID:    leasedJob.Id(),
					JobSet:   "testJobSet",
					Executor: "testExecutor",
					Failed:   true,
					Serial:   1,
				},
			},
			jobRunErrors: map[string]*armadaevents.Error{
				leasedJob.LatestRun().Id(): defaultJobError,
			},
			expectedJobErrors:     []string{leasedJob.Id()},
			expectedTerminal:      []string{leasedJob.Id()},
			expectedQueuedVersion: leasedJob.QueuedVersion(),
		},
		"Job succeeded": {
			initialJobs: []*jobdb.Job{leasedJob},
			runUpdates: []database.Run{
				{
					RunID:     leasedJob.LatestRun().Id(),
					JobID:     leasedJob.Id(),
					JobSet:    "testJobSet",
					Executor:  "testExecutor",
					Succeeded: true,
					Serial:    1,
				},
			},
			expectedJobSucceeded:  []string{leasedJob.Id()},
			expectedTerminal:      []string{leasedJob.Id()},
			expectedQueuedVersion: leasedJob.QueuedVersion(),
		},
		"Job preempted": {
			initialJobs:               []*jobdb.Job{leasedJob},
			expectedJobsRunsToPreempt: []string{leasedJob.Id()},
			expectedJobRunPreempted:   []string{leasedJob.Id()},
			expectedJobErrors:         []string{leasedJob.Id()},
			expectedJobRunErrors:      []string{leasedJob.Id()},
			expectedTerminal:          []string{leasedJob.Id()},
			expectedQueuedVersion:     leasedJob.QueuedVersion(),
		},
		"Fetch fails": {
			initialJobs:           []*jobdb.Job{leasedJob},
			fetchError:            true,
			expectedLeased:        []string{leasedJob.Id()},
			expectedQueuedVersion: leasedJob.QueuedVersion(),
		},
		"Schedule fails": {
			initialJobs:           []*jobdb.Job{leasedJob},
			scheduleError:         true,
			expectedLeased:        []string{leasedJob.Id()}, // job should still be leased as error was thrown and transaction rolled back
			expectedQueuedVersion: leasedJob.QueuedVersion(),
		},
		"Publish fails": {
			initialJobs:           []*jobdb.Job{leasedJob},
			publishError:          true,
			expectedLeased:        []string{leasedJob.Id()}, // job should still be leased as error was thrown and transaction rolled back
			expectedQueuedVersion: leasedJob.QueuedVersion(),
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			clusterTimeout := 1 * time.Hour

			// Test objects
			jobRepo := &testJobRepository{
				updatedJobs: tc.jobUpdates,
				updatedRuns: tc.runUpdates,
				errors:      tc.jobRunErrors,
				shouldError: tc.fetchError,
			}
			testClock := clock.NewFakeClock(time.Now())
			schedulingAlgo := &testSchedulingAlgo{
				jobsToSchedule: tc.expectedJobRunLeased,
				jobsToPreempt:  tc.expectedJobsRunsToPreempt,
				shouldError:    tc.scheduleError,
			}
			publisher := &testPublisher{shouldError: tc.publishError}
			submitChecker := &testSubmitChecker{checkSuccess: !tc.submitCheckerFailure}

			heartbeatTime := testClock.Now()
			if tc.staleExecutor {
				heartbeatTime = heartbeatTime.Add(-2 * clusterTimeout)
			}
			clusterRepo := &testExecutorRepository{
				updateTimes: map[string]time.Time{"testExecutor": heartbeatTime},
			}
			sched, err := NewScheduler(
				testfixtures.NewJobDb(testfixtures.TestResourceListFactory),
				jobRepo,
				clusterRepo,
				schedulingAlgo,
				leader.NewStandaloneLeaderController(),
				publisher,
				submitChecker,
				1*time.Second,
				5*time.Second,
				clusterTimeout,
				nil,
				maxNumberOfAttempts,
				nodeIdLabel,
				schedulerMetrics,
				pricing.NoopBidPriceProvider{},
				[]string{},
			)
			require.NoError(t, err)
			sched.EnableAssertions()

			sched.clock = testClock

			// insert initial jobs
			txn := sched.jobDb.WriteTxn()
			err = txn.Upsert(tc.initialJobs)
			require.NoError(t, err)
			txn.Commit()

			// run a scheduler cycle
			ctx, cancel := armadacontext.WithTimeout(armadacontext.Background(), 5*time.Second)
			_, err = sched.cycle(ctx, false, sched.leaderController.GetToken(), true)
			if tc.fetchError || tc.publishError || tc.scheduleError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// Assert that all expected eventSequences are generated and that all eventSequences are expected.
			outstandingEventsByType := map[string]map[string]bool{
				fmt.Sprintf("%T", &armadaevents.EventSequence_Event_JobRunLeased{}):     stringSet(tc.expectedJobRunLeased),
				fmt.Sprintf("%T", &armadaevents.EventSequence_Event_JobErrors{}):        stringSet(tc.expectedJobErrors),
				fmt.Sprintf("%T", &armadaevents.EventSequence_Event_JobRunErrors{}):     stringSet(tc.expectedJobRunErrors),
				fmt.Sprintf("%T", &armadaevents.EventSequence_Event_JobRunPreempted{}):  stringSet(tc.expectedJobRunPreempted),
				fmt.Sprintf("%T", &armadaevents.EventSequence_Event_JobRunCancelled{}):  stringSet(tc.expectedJobRunCancelled),
				fmt.Sprintf("%T", &armadaevents.EventSequence_Event_CancelledJob{}):     stringSet(tc.expectedJobCancelled),
				fmt.Sprintf("%T", &armadaevents.EventSequence_Event_ReprioritisedJob{}): stringSet(tc.expectedJobReprioritised),
				fmt.Sprintf("%T", &armadaevents.EventSequence_Event_JobSucceeded{}):     stringSet(tc.expectedJobSucceeded),
				fmt.Sprintf("%T", &armadaevents.EventSequence_Event_JobRequeued{}):      stringSet(tc.expectedRequeued),
				fmt.Sprintf("%T", &armadaevents.EventSequence_Event_CancelJob{}):        stringSet(tc.expectedJobRequestCancel),
				fmt.Sprintf("%T", &armadaevents.EventSequence_Event_JobValidated{}):     stringSet(tc.expectedValidated),
			}
			err = subtractEventsFromOutstandingEventsByType(publisher.eventSequences, outstandingEventsByType)
			require.NoError(t, err)
			for eventType, m := range outstandingEventsByType {
				assert.Empty(t, m, "%d outstanding eventSequences of type %s", len(m), eventType)
			}

			// assert that the serials are where we expect them to be
			if len(tc.jobUpdates) > 0 {
				assert.Equal(t, tc.jobUpdates[len(tc.jobUpdates)-1].Serial, sched.jobsSerial)
			} else {
				assert.Equal(t, int64(-1), sched.jobsSerial)
			}
			if len(tc.runUpdates) > 0 {
				assert.Equal(t, tc.runUpdates[len(tc.runUpdates)-1].Serial, sched.runsSerial)
			} else {
				assert.Equal(t, int64(-1), sched.runsSerial)
			}

			// assert that the job db is in the state we expect
			jobs := sched.jobDb.ReadTxn().GetAll()
			remainingLeased := stringSet(tc.expectedLeased)
			remainingQueued := stringSet(tc.expectedQueued)
			remainingTerminal := stringSet(tc.expectedTerminal)
			for _, job := range jobs {
				if job.InTerminalState() {
					_, ok := remainingTerminal[job.Id()]
					assert.True(t, ok)
					allRunsTerminal := true
					for _, run := range job.AllRuns() {
						if !run.InTerminalState() {
							allRunsTerminal = false
						}
					}
					assert.True(t, allRunsTerminal)
					delete(remainingTerminal, job.Id())
				} else if job.Queued() {
					_, ok := remainingQueued[job.Id()]
					assert.True(t, ok)
					delete(remainingQueued, job.Id())
				} else {
					_, ok := remainingLeased[job.Id()]
					assert.True(t, ok)
					delete(remainingLeased, job.Id())
				}
				if expectedPriority, ok := tc.expectedJobPriority[job.Id()]; ok {
					assert.Equal(t, expectedPriority, job.Priority())
				}
				if len(tc.expectedNodeAntiAffinities) > 0 {
					affinity := job.JobSchedulingInfo().PodRequirements.Affinity
					assert.NotNil(t, affinity)
					expectedAffinity := createAntiAffinity(t, nodeIdLabel, tc.expectedNodeAntiAffinities)
					assert.Equal(t, expectedAffinity, affinity)
				}
				podRequirements := job.PodRequirements()
				assert.NotNil(t, podRequirements)

				assert.Equal(t, tc.expectedQueuedVersion, job.QueuedVersion())
				expectedSchedulingInfoVersion := 1
				if tc.expectedJobSchedulingInfoVersion != 0 {
					expectedSchedulingInfoVersion = tc.expectedJobSchedulingInfoVersion
				}
				assert.Equal(t, uint32(expectedSchedulingInfoVersion), job.JobSchedulingInfo().Version)
			}
			assert.Equal(t, 0, len(remainingLeased))
			assert.Equal(t, 0, len(remainingQueued))
			assert.Equal(t, 0, len(remainingTerminal))
			cancel()
		})
	}
}

func createAntiAffinity(t *testing.T, key string, values []string) *v1.Affinity {
	newAffinity := &v1.Affinity{}
	for _, value := range values {
		err := affinity.AddNodeAntiAffinity(newAffinity, key, value)
		assert.NoError(t, err)
	}
	return newAffinity
}

func subtractEventsFromOutstandingEventsByType(eventSequences []*armadaevents.EventSequence, outstandingEventsByType map[string]map[string]bool) error {
	for _, eventSequence := range eventSequences {
		for _, event := range eventSequence.Events {
			jobId, err := armadaevents.JobIdFromEvent(event)
			if err != nil {
				return err
			}
			key := fmt.Sprintf("%T", event.Event)
			_, ok := outstandingEventsByType[key][jobId]
			if !ok {
				return errors.Errorf("received unexpected event for job %s: %T - %v", jobId, event.Event, event.Event)
			}
			delete(outstandingEventsByType[key], jobId)
		}
	}
	return nil
}

// Test running multiple scheduler cycles
func TestRun(t *testing.T) {
	// Test objects
	jobRepo := testJobRepository{numReceivedPartitions: 100}
	testClock := clock.NewFakeClock(time.Now())
	schedulingAlgo := &testSchedulingAlgo{}
	publisher := &testPublisher{}
	clusterRepo := &testExecutorRepository{}
	leaderController := leader.NewStandaloneLeaderController()
	submitChecker := &testSubmitChecker{checkSuccess: true}
	sched, err := NewScheduler(
		testfixtures.NewJobDb(testfixtures.TestResourceListFactory),
		&jobRepo,
		clusterRepo,
		schedulingAlgo,
		leaderController,
		publisher,
		submitChecker,
		1*time.Second,
		15*time.Second,
		1*time.Hour,
		nil,
		maxNumberOfAttempts,
		nodeIdLabel,
		schedulerMetrics,
		pricing.NoopBidPriceProvider{},
		[]string{},
	)
	require.NoError(t, err)
	sched.EnableAssertions()

	sched.clock = testClock

	ctx, cancel := armadacontext.WithCancel(armadacontext.Background())

	//nolint:errcheck
	go sched.Run(ctx)

	time.Sleep(1 * time.Second)

	// Function that runs a cycle and waits until it sees published messages
	fireCycle := func() {
		publisher.Reset()
		wg := sync.WaitGroup{}
		wg.Add(1)
		sched.onCycleCompleted = func() { wg.Done() }
		jobId := util.NewULID()
		jobRepo.updatedJobs = []database.Job{{JobID: jobId, Queue: "testQueue", Queued: true, Validated: true, SchedulingInfo: schedulingInfoBytes}}
		schedulingAlgo.jobsToSchedule = []string{jobId}
		testClock.Step(10 * time.Second)
		wg.Wait()
	}

	// fire a cycle and assert that we became leader and published
	fireCycle()
	assert.Equal(t, 1, len(publisher.eventSequences))
	assert.Equal(t, schedulingAlgo.numberOfScheduleCalls, 1)

	// invalidate our leadership: we should not publish
	leaderController.SetToken(leader.InvalidLeaderToken())
	fireCycle()
	assert.Equal(t, 0, len(publisher.eventSequences))
	assert.Equal(t, schedulingAlgo.numberOfScheduleCalls, 1)

	// become master again: we should publish
	leaderController.SetToken(leader.NewLeaderToken())
	fireCycle()
	assert.Equal(t, 1, len(publisher.eventSequences))
	assert.Equal(t, schedulingAlgo.numberOfScheduleCalls, 2)

	cancel()
}

// Test job pricing data is updated through subsequent scheduling rounds
func TestJobPriceUpdates(t *testing.T) {
	queuedJob := database.Job{JobID: util.NewULID(), Queue: "testQueue", Queued: true, Validated: true, Pools: []string{testfixtures.TestPool}, SchedulingInfo: schedulingInfoBytes, PriceBand: 2}
	runningJob := database.Job{JobID: util.NewULID(), Queue: "testQueue", Queued: false, Validated: true, Pools: []string{testfixtures.TestPool}, SchedulingInfo: schedulingInfoBytes, PriceBand: 2}
	queuedJobNoPricingInfo := database.Job{JobID: util.NewULID(), Queue: "testQueue2", Queued: true, Validated: true, Pools: []string{testfixtures.TestPool}, SchedulingInfo: schedulingInfoBytes, PriceBand: 2}
	runningJobNoPricingInfo := database.Job{JobID: util.NewULID(), Queue: "testQueue2", Queued: false, Validated: true, Pools: []string{testfixtures.TestPool}, SchedulingInfo: schedulingInfoBytes, PriceBand: 2}
	queuedPreemptibleJob := database.Job{JobID: util.NewULID(), Queue: "testQueue", Queued: true, Validated: true, Pools: []string{testfixtures.TestPool}, SchedulingInfo: preemptibleSchedulingInfoBytes, PriceBand: 2}
	runningPreemptibleJob := database.Job{JobID: util.NewULID(), Queue: "testQueue", Queued: false, Validated: true, Pools: []string{testfixtures.TestPool}, SchedulingInfo: preemptibleSchedulingInfoBytes, PriceBand: 2}
	queuedPreemptibleJobNoPricingInfo := database.Job{JobID: util.NewULID(), Queue: "testQueue2", Queued: true, Validated: true, Pools: []string{testfixtures.TestPool}, SchedulingInfo: preemptibleSchedulingInfoBytes, PriceBand: 2}
	runningPreemptibleJobNoPricingInfo := database.Job{JobID: util.NewULID(), Queue: "testQueue2", Queued: false, Validated: true, Pools: []string{testfixtures.TestPool}, SchedulingInfo: preemptibleSchedulingInfoBytes, PriceBand: 2}
	jobWithoutMarketDrivenPools := database.Job{JobID: util.NewULID(), Queue: "testQueue", Queued: true, Validated: true, Pools: []string{"other"}, SchedulingInfo: schedulingInfoBytes}

	expectedInitialBid := map[string]pricing.Bid{testfixtures.TestPool: {QueuedBid: 2, RunningBid: 2}}
	expectedUpdatedBid := map[string]pricing.Bid{testfixtures.TestPool: {QueuedBid: 3, RunningBid: 3}}

	// Test runs for 3 rounds
	// - 1. Run as leader
	// - 2. Run as follower, prices updated
	// - 3. Run as leader
	tests := map[string]struct {
		marketDrivenPools             []string
		initialJob                    database.Job
		expectedNumberOfProviderCalls []int
		expectedJobBid                []map[string]pricing.Bid
		expectedJobBidPrice           []float64
	}{
		"no market driven pools": {
			// No market driven pools, so prices shouldn't be getting updated
			marketDrivenPools:             []string{},
			initialJob:                    queuedJob,
			expectedNumberOfProviderCalls: []int{0, 0, 0},
			expectedJobBid:                []map[string]pricing.Bid{nil, nil, nil},
			expectedJobBidPrice:           []float64{0, 0, 0},
		},
		"job with no market driven pools": {
			// No need to set price on job that doesn't belong to market driven pools
			marketDrivenPools:             []string{testfixtures.TestPool},
			initialJob:                    jobWithoutMarketDrivenPools,
			expectedNumberOfProviderCalls: []int{1, 1, 2},
			expectedJobBid:                []map[string]pricing.Bid{nil, nil, nil},
			expectedJobBidPrice:           []float64{0, 0, 0},
		},
		"non-preemptible queued job": {
			// No market driven pools, so prices shouldn't be getting updated
			marketDrivenPools:             []string{testfixtures.TestPool},
			initialJob:                    queuedJob,
			expectedNumberOfProviderCalls: []int{1, 1, 2},
			expectedJobBid:                []map[string]pricing.Bid{expectedInitialBid, expectedInitialBid, expectedUpdatedBid},
			expectedJobBidPrice:           []float64{2, 2, 3},
		},
		"non-preemptible running job": {
			// No market driven pools, so prices shouldn't be getting updated
			marketDrivenPools:             []string{testfixtures.TestPool},
			initialJob:                    runningJob,
			expectedNumberOfProviderCalls: []int{1, 1, 2},
			expectedJobBid:                []map[string]pricing.Bid{expectedInitialBid, expectedInitialBid, expectedUpdatedBid},
			expectedJobBidPrice:           []float64{pricing.NonPreemptibleRunningPrice, pricing.NonPreemptibleRunningPrice, pricing.NonPreemptibleRunningPrice},
		},
		"non-preemptible queued job with no pricing info": {
			// No market driven pools, so prices shouldn't be getting updated
			marketDrivenPools:             []string{testfixtures.TestPool},
			initialJob:                    queuedJobNoPricingInfo,
			expectedNumberOfProviderCalls: []int{1, 1, 2},
			expectedJobBid:                []map[string]pricing.Bid{nil, nil, nil},
			expectedJobBidPrice:           []float64{0, 0, 0},
		},
		"non-preemptible running job with no pricing info": {
			// No market driven pools, so prices shouldn't be getting updated
			marketDrivenPools:             []string{testfixtures.TestPool},
			initialJob:                    runningJobNoPricingInfo,
			expectedNumberOfProviderCalls: []int{1, 1, 2},
			expectedJobBid:                []map[string]pricing.Bid{nil, nil, nil},
			expectedJobBidPrice:           []float64{pricing.NonPreemptibleRunningPrice, pricing.NonPreemptibleRunningPrice, pricing.NonPreemptibleRunningPrice},
		},
		"preemptible queued job": {
			// No market driven pools, so prices shouldn't be getting updated
			marketDrivenPools:             []string{testfixtures.TestPool},
			initialJob:                    queuedPreemptibleJob,
			expectedNumberOfProviderCalls: []int{1, 1, 2},
			expectedJobBid:                []map[string]pricing.Bid{expectedInitialBid, expectedInitialBid, expectedUpdatedBid},
			expectedJobBidPrice:           []float64{2, 2, 3},
		},
		"preemptible running job": {
			// No market driven pools, so prices shouldn't be getting updated
			marketDrivenPools:             []string{testfixtures.TestPool},
			initialJob:                    runningPreemptibleJob,
			expectedNumberOfProviderCalls: []int{1, 1, 2},
			expectedJobBid:                []map[string]pricing.Bid{expectedInitialBid, expectedInitialBid, expectedUpdatedBid},
			expectedJobBidPrice:           []float64{2, 2, 3},
		},
		"preemptible queued job - no pricing info": {
			// No market driven pools, so prices shouldn't be getting updated
			marketDrivenPools:             []string{testfixtures.TestPool},
			initialJob:                    queuedPreemptibleJobNoPricingInfo,
			expectedNumberOfProviderCalls: []int{1, 1, 2},
			expectedJobBid:                []map[string]pricing.Bid{nil, nil, nil},
			expectedJobBidPrice:           []float64{0, 0, 0},
		},
		"preemptible running job - no pricing info": {
			// No market driven pools, so prices shouldn't be getting updated
			marketDrivenPools:             []string{testfixtures.TestPool},
			initialJob:                    runningPreemptibleJobNoPricingInfo,
			expectedNumberOfProviderCalls: []int{1, 1, 2},
			expectedJobBid:                []map[string]pricing.Bid{nil, nil, nil},
			expectedJobBidPrice:           []float64{0, 0, 0},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Test objects
			jobDb := testfixtures.NewJobDb(testfixtures.TestResourceListFactory)
			jobRepo := testJobRepository{numReceivedPartitions: 100}
			testClock := clock.NewFakeClock(time.Now())
			schedulingAlgo := &testSchedulingAlgo{}
			publisher := &testPublisher{}
			priceProvider := &testBidPriceProvider{
				pools:  tc.marketDrivenPools,
				queues: []string{"testQueue"},
				priceFunc: func(band bidstore.PriceBand) float64 {
					return float64(band)
				},
			}
			clusterRepo := &testExecutorRepository{}
			leaderController := leader.NewStandaloneLeaderController()
			submitChecker := &testSubmitChecker{checkSuccess: true}
			sched, err := NewScheduler(
				jobDb,
				&jobRepo,
				clusterRepo,
				schedulingAlgo,
				leaderController,
				publisher,
				submitChecker,
				1*time.Second,
				15*time.Second,
				1*time.Hour,
				nil,
				maxNumberOfAttempts,
				nodeIdLabel,
				schedulerMetrics,
				priceProvider,
				tc.marketDrivenPools,
			)
			require.NoError(t, err)

			sched.clock = testClock

			ctx, cancel := armadacontext.WithCancel(armadacontext.Background())
			jobRepo.initialJobs = []database.Job{tc.initialJob}

			//nolint:errcheck
			go sched.Run(ctx)

			time.Sleep(1 * time.Second)

			// Function that runs a cycle and waits until it sees published messages
			fireCycle := func() {
				publisher.Reset()
				wg := sync.WaitGroup{}
				wg.Add(1)
				sched.onCycleCompleted = func() { wg.Done() }
				testClock.Step(10 * time.Second)
				wg.Wait()
			}

			// fire a cycle and assert that we became leader and published
			fireCycle()
			currentJob := jobDb.ReadTxn().GetById(tc.initialJob.JobID)
			assert.NotNil(t, currentJob)
			assert.Equal(t, tc.expectedJobBid[0], currentJob.GetAllBidPrices())
			assert.Equal(t, tc.expectedJobBidPrice[0], currentJob.GetBidPrice(testfixtures.TestPool))
			assert.Equal(t, priceProvider.numberOfCalls, tc.expectedNumberOfProviderCalls[0])

			// invalidate our leadership: we shouldn't update prices
			leaderController.SetToken(leader.InvalidLeaderToken())
			priceProvider.priceFunc = func(band bidstore.PriceBand) float64 {
				return float64(band) + 1
			}
			fireCycle()
			currentJob = jobDb.ReadTxn().GetById(tc.initialJob.JobID)
			assert.NotNil(t, currentJob)
			assert.Equal(t, tc.expectedJobBid[1], currentJob.GetAllBidPrices())
			assert.Equal(t, tc.expectedJobBidPrice[1], currentJob.GetBidPrice(testfixtures.TestPool))
			assert.Equal(t, priceProvider.numberOfCalls, tc.expectedNumberOfProviderCalls[1])

			// become master again: we shouldn't update prices
			leaderController.SetToken(leader.NewLeaderToken())
			fireCycle()
			currentJob = jobDb.ReadTxn().GetById(tc.initialJob.JobID)
			assert.NotNil(t, currentJob)
			assert.Equal(t, tc.expectedJobBid[2], currentJob.GetAllBidPrices())
			assert.Equal(t, tc.expectedJobBidPrice[2], currentJob.GetBidPrice(testfixtures.TestPool))
			assert.Equal(t, priceProvider.numberOfCalls, tc.expectedNumberOfProviderCalls[2])

			cancel()
		})
	}
}

func TestScheduler_TestSyncInitialState(t *testing.T) {
	var maxJobSerial int64 = 5
	var maxRunSerial int64 = 3
	tests := map[string]struct {
		jobRepo                  *testJobRepository
		expectedInitialJobs      []*jobdb.Job
		expectedInitialJobDbIds  []string
		expectedJobsSerial       int64
		expectedRunsSerial       int64
		expectedInitialJobSerial int64
		expectedInitialRunSerial int64
	}{
		"empty db": {
			jobRepo:                  &testJobRepository{},
			expectedInitialJobs:      []*jobdb.Job{},
			expectedInitialJobDbIds:  []string{},
			expectedJobsSerial:       -1,
			expectedRunsSerial:       -1,
			expectedInitialJobSerial: -1,
			expectedInitialRunSerial: -1,
		},
		"no initial jobs": {
			jobRepo: &testJobRepository{
				maxJobSerial: &maxJobSerial,
				maxRunSerial: &maxRunSerial,
			},
			expectedInitialJobs:      []*jobdb.Job{},
			expectedInitialJobDbIds:  []string{},
			expectedJobsSerial:       5,
			expectedRunsSerial:       3,
			expectedInitialJobSerial: 5,
			expectedInitialRunSerial: 3,
		},
		"initial jobs are present": {
			jobRepo: &testJobRepository{
				initialJobs: []database.Job{
					{
						JobID:          queuedJob.Id(),
						JobSet:         queuedJob.Jobset(),
						Queue:          queuedJob.Queue(),
						Queued:         false,
						QueuedVersion:  1,
						Priority:       int64(queuedJob.Priority()),
						SchedulingInfo: schedulingInfoBytes,
						Serial:         1,
						Validated:      true,
						Submitted:      1,
						PriceBand:      3,
					},
				},
				initialRuns: []database.Run{
					{
						RunID:    leasedJob.LatestRun().Id(),
						JobID:    queuedJob.Id(),
						JobSet:   queuedJob.Jobset(),
						Executor: "test-executor",
						Node:     "test-node",
						Created:  123,
						ScheduledAtPriority: func() *int32 {
							scheduledAtPriority := int32(5)
							return &scheduledAtPriority
						}(),
						Serial: 1,
					},
				},
				maxJobSerial: &maxJobSerial,
				maxRunSerial: &maxRunSerial,
			},
			expectedInitialJobs: []*jobdb.Job{
				queuedJob.WithUpdatedRun(
					testfixtures.JobDb.CreateRun(
						leasedJob.LatestRun().Id(),
						queuedJob.Id(),
						123,
						"test-executor",
						"test-executor-test-node",
						"test-node",
						"pool",
						pointer.Int32(5),
						false,
						false,
						false,
						false,
						false,
						false,
						false,
						false,
						nil,
						nil,
						nil,
						nil,
						nil,
						false,
						false,
					)).WithQueued(false).WithQueuedVersion(1).WithPriceBand(bidstore.PriceBand_PRICE_BAND_C),
			},
			expectedInitialJobDbIds: []string{queuedJob.Id()},
			expectedJobsSerial:      1,
			expectedRunsSerial:      1,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := armadacontext.WithTimeout(armadacontext.Background(), 5*time.Second)
			defer cancel()

			// Test objects
			schedulingAlgo := &testSchedulingAlgo{}
			publisher := &testPublisher{}
			clusterRepo := &testExecutorRepository{}
			leaderController := leader.NewStandaloneLeaderController()
			sched, err := NewScheduler(
				testfixtures.NewJobDb(testfixtures.TestResourceListFactory),
				tc.jobRepo,
				clusterRepo,
				schedulingAlgo,
				leaderController,
				publisher,
				nil,
				1*time.Second,
				5*time.Second,
				1*time.Hour,
				nil,
				maxNumberOfAttempts,
				nodeIdLabel,
				schedulerMetrics,
				pricing.NoopBidPriceProvider{},
				[]string{},
			)
			require.NoError(t, err)
			sched.EnableAssertions()

			// The SchedulingKeyGenerator embedded in the jobDb has some randomness,
			// which must be consistent within tests.
			sched.jobDb = testfixtures.NewJobDb(testfixtures.TestResourceListFactory)

			initialJobs, _, err := sched.syncState(ctx, true)
			require.NoError(t, err)

			expectedJobDb := testfixtures.NewJobDbWithJobs(tc.expectedInitialJobs)
			actualJobDb := testfixtures.NewJobDbWithJobs(initialJobs)
			assert.NoError(t, expectedJobDb.ReadTxn().AssertEqual(actualJobDb.ReadTxn()))
			allDbJobs := sched.jobDb.ReadTxn().GetAll()

			expectedIds := stringSet(tc.expectedInitialJobDbIds)
			require.Equal(t, len(tc.expectedInitialJobDbIds), len(allDbJobs))
			for _, job := range allDbJobs {
				_, ok := expectedIds[job.Id()]
				assert.True(t, ok)
			}

			require.Equal(t, tc.expectedJobsSerial, sched.jobsSerial)
			require.Equal(t, tc.expectedRunsSerial, sched.runsSerial)
		})
	}
}

func TestScheduler_TestSyncState(t *testing.T) {
	tests := map[string]struct {
		initialJobs         []*jobdb.Job   // jobs in the jobdb at the start of the cycle
		jobUpdates          []database.Job // job updates from the database
		runUpdates          []database.Run // run updates from the database
		expectedUpdatedJobs []*jobdb.Job
		expectedJobDbIds    []string
	}{
		"insert job": {
			jobUpdates: []database.Job{
				{
					JobID:          queuedJob.Id(),
					JobSet:         queuedJob.Jobset(),
					Queue:          queuedJob.Queue(),
					Submitted:      queuedJob.Created(),
					Queued:         true,
					QueuedVersion:  0,
					Priority:       int64(queuedJob.Priority()),
					SchedulingInfo: schedulingInfoBytes,
					Serial:         1,
					Validated:      true,
				},
			},
			expectedUpdatedJobs: []*jobdb.Job{queuedJob},
			expectedJobDbIds:    []string{queuedJob.Id()},
		},
		"insert job that already exists": {
			initialJobs: []*jobdb.Job{queuedJob},
			jobUpdates: []database.Job{
				{
					JobID:          queuedJob.Id(),
					JobSet:         queuedJob.Jobset(),
					Queue:          queuedJob.Queue(),
					Submitted:      queuedJob.Created(),
					Priority:       int64(queuedJob.Priority()),
					SchedulingInfo: schedulingInfoBytes,
					Serial:         1,
				},
			},
			expectedUpdatedJobs: []*jobdb.Job{queuedJob},
			expectedJobDbIds:    []string{queuedJob.Id()},
		},
		"add job run": {
			initialJobs: []*jobdb.Job{queuedJob},
			jobUpdates: []database.Job{
				{
					JobID:          queuedJob.Id(),
					JobSet:         queuedJob.Jobset(),
					Queue:          queuedJob.Queue(),
					Queued:         false,
					QueuedVersion:  2,
					Priority:       int64(queuedJob.Priority()),
					SchedulingInfo: schedulingInfoBytes,
					Serial:         2,
				},
			},
			runUpdates: []database.Run{
				{
					RunID:    leasedJob.LatestRun().Id(),
					JobID:    queuedJob.Id(),
					JobSet:   queuedJob.Jobset(),
					Executor: "test-executor",
					Node:     "test-node",
					Created:  123,
					ScheduledAtPriority: func() *int32 {
						scheduledAtPriority := int32(5)
						return &scheduledAtPriority
					}(),
				},
			},
			expectedUpdatedJobs: []*jobdb.Job{
				queuedJob.WithUpdatedRun(
					testfixtures.JobDb.CreateRun(
						leasedJob.LatestRun().Id(),
						queuedJob.Id(),
						123,
						"test-executor",
						"test-executor-test-node",
						"test-node",
						"pool",
						pointer.Int32(5),
						false,
						false,
						false,
						false,
						false,
						false,
						false,
						false,
						nil,
						nil,
						nil,
						nil,
						nil,
						false,
						false,
					)).WithQueued(false).WithQueuedVersion(2),
			},
			expectedJobDbIds: []string{queuedJob.Id()},
		},
		"job succeeded": {
			initialJobs: []*jobdb.Job{leasedJob},
			jobUpdates: []database.Job{
				{
					JobID:          leasedJob.Id(),
					JobSet:         leasedJob.Jobset(),
					Queue:          leasedJob.Queue(),
					Submitted:      leasedJob.Created(),
					Priority:       int64(leasedJob.Priority()),
					SchedulingInfo: schedulingInfoBytes,
					Succeeded:      true,
					Serial:         1,
				},
			},
			runUpdates: []database.Run{
				{
					RunID:     leasedJob.LatestRun().Id(),
					JobID:     leasedJob.LatestRun().JobId(),
					JobSet:    leasedJob.Jobset(),
					Succeeded: true,
				},
			},
			expectedUpdatedJobs: []*jobdb.Job{leasedJob.
				WithUpdatedRun(leasedJob.LatestRun().WithSucceeded(true)).
				WithSucceeded(true)},
			expectedJobDbIds: []string{},
		},
		"job requeued": {
			initialJobs: []*jobdb.Job{leasedJob},
			jobUpdates: []database.Job{
				{
					JobID:                 leasedJob.Id(),
					JobSet:                leasedJob.Jobset(),
					Queue:                 leasedJob.Queue(),
					Submitted:             leasedJob.Created(),
					Queued:                true,
					QueuedVersion:         3,
					Priority:              int64(leasedJob.Priority()),
					SchedulingInfo:        updatedSchedulingInfoBytes,
					SchedulingInfoVersion: int32(updatedSchedulingInfo.Version),
					Serial:                1,
				},
			},
			expectedUpdatedJobs: []*jobdb.Job{
				jobdb.JobWithJobSchedulingInfo(leasedJob, toInternalSchedulingInfo(updatedSchedulingInfo)).
					WithQueued(true).
					WithQueuedVersion(3),
			},
			expectedJobDbIds: []string{leasedJob.Id()},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := armadacontext.WithTimeout(armadacontext.Background(), 5*time.Second)
			defer cancel()

			// Test objects
			jobRepo := &testJobRepository{
				updatedJobs: tc.jobUpdates,
				updatedRuns: tc.runUpdates,
			}
			schedulingAlgo := &testSchedulingAlgo{}
			publisher := &testPublisher{}
			clusterRepo := &testExecutorRepository{}
			leaderController := leader.NewStandaloneLeaderController()
			sched, err := NewScheduler(
				testfixtures.NewJobDb(testfixtures.TestResourceListFactory),
				jobRepo,
				clusterRepo,
				schedulingAlgo,
				leaderController,
				publisher,
				nil,
				1*time.Second,
				5*time.Second,
				1*time.Hour,
				nil,
				maxNumberOfAttempts,
				nodeIdLabel,
				schedulerMetrics,
				pricing.NoopBidPriceProvider{},
				[]string{},
			)
			require.NoError(t, err)
			sched.EnableAssertions()

			// The SchedulingKeyGenerator embedded in the jobDb has some randomness,
			// which must be consistent within tests.
			sched.jobDb = testfixtures.NewJobDb(testfixtures.TestResourceListFactory)

			// insert initial jobs
			txn := sched.jobDb.WriteTxn()
			err = txn.Upsert(tc.initialJobs)
			require.NoError(t, err)
			txn.Commit()

			updatedJobs, _, err := sched.syncState(ctx, false)
			require.NoError(t, err)

			expectedJobDb := testfixtures.NewJobDbWithJobs(tc.expectedUpdatedJobs)
			actualJobDb := testfixtures.NewJobDbWithJobs(updatedJobs)
			assert.NoError(t, expectedJobDb.ReadTxn().AssertEqual(actualJobDb.ReadTxn()))
			allDbJobs := sched.jobDb.ReadTxn().GetAll()

			expectedIds := stringSet(tc.expectedJobDbIds)
			require.Equal(t, len(tc.expectedJobDbIds), len(allDbJobs))
			for _, job := range allDbJobs {
				_, ok := expectedIds[job.Id()]
				assert.True(t, ok)
			}
		})
	}
}

type testSubmitChecker struct {
	checkSuccess bool
}

func (t *testSubmitChecker) Check(_ *armadacontext.Context, jobs []*jobdb.Job) (map[string]schedulingResult, error) {
	result := make(map[string]schedulingResult)
	for _, job := range jobs {
		if t.checkSuccess {
			result[job.Id()] = schedulingResult{isSchedulable: true}
		} else {
			result[job.Id()] = schedulingResult{isSchedulable: false, reason: "job not schedulable"}
		}
	}
	return result, nil
}

// Test implementations of the interfaces needed by the Scheduler
type testJobRepository struct {
	initialJobs           []database.Job
	initialRuns           []database.Run
	updatedJobs           []database.Job
	updatedRuns           []database.Run
	errors                map[string]*armadaevents.Error
	shouldError           bool
	numReceivedPartitions uint32
	maxJobSerial          *int64
	maxRunSerial          *int64
}

func (t *testJobRepository) FindInactiveRuns(ctx *armadacontext.Context, runIds []string) ([]string, error) {
	// TODO implement me
	panic("implement me")
}

func (t *testJobRepository) FetchJobRunLeases(ctx *armadacontext.Context, executor string, maxResults uint, excludedRunIds []string) ([]*database.JobRunLease, error) {
	// TODO implement me
	panic("implement me")
}

func (t *testJobRepository) FetchJobUpdates(ctx *armadacontext.Context, jobSerial int64, jobRunSerial int64) ([]database.Job, []database.Run, error) {
	if t.shouldError {
		return nil, nil, errors.New("error fetchiung job updates")
	}
	return t.updatedJobs, t.updatedRuns, nil
}

func (t *testJobRepository) FetchJobRunErrors(ctx *armadacontext.Context, runIds []string) (map[string]*armadaevents.Error, error) {
	if t.shouldError {
		return nil, errors.New("error fetching job run errors")
	}
	return t.errors, nil
}

func (t *testJobRepository) CountReceivedPartitions(ctx *armadacontext.Context, groupId uuid.UUID) (uint32, error) {
	if t.shouldError {
		return 0, errors.New("error counting received partitions")
	}
	return t.numReceivedPartitions, nil
}

func (t *testJobRepository) FetchInitialJobs(ctx *armadacontext.Context) ([]database.Job, []database.Run, *int64, *int64, error) {
	if t.shouldError {
		return nil, nil, nil, nil, errors.New("error fetching job updates")
	}
	return t.initialJobs, t.initialRuns, t.maxJobSerial, t.maxRunSerial, nil
}

type testExecutorRepository struct {
	updateTimes map[string]time.Time
	shouldError bool
}

func (t testExecutorRepository) GetExecutors(ctx *armadacontext.Context) ([]*schedulerobjects.Executor, error) {
	panic("not implemented")
}

func (t testExecutorRepository) GetExecutorSettings(ctx *armadacontext.Context) ([]*schedulerobjects.ExecutorSettings, error) {
	panic("not implemented")
}

func (t testExecutorRepository) GetLastUpdateTimes(ctx *armadacontext.Context) (map[string]time.Time, error) {
	if t.shouldError {
		return nil, errors.New("error getting last update time")
	}
	return t.updateTimes, nil
}

func (t testExecutorRepository) StoreExecutor(ctx *armadacontext.Context, executor *schedulerobjects.Executor) error {
	panic("not implemented")
}

type testSchedulingAlgo struct {
	numberOfScheduleCalls int
	jobsToPreempt         []string
	jobsToSchedule        []string
	shouldError           bool
	// Set to true to indicate that preemption/scheduling/failure decisions have been persisted.
	// Until persisted is set to true, the same jobs are preempted/scheduled/failed on every call.
	persisted bool
}

func (t *testSchedulingAlgo) Schedule(_ *armadacontext.Context, _ map[string]internaltypes.ResourceList, txn *jobdb.Txn) (*scheduling.SchedulerResult, error) {
	t.numberOfScheduleCalls++
	if t.shouldError {
		return nil, errors.New("error scheduling jobs")
	}
	if t.persisted {
		// Exit right away if decisions have already been persisted.
		return &scheduling.SchedulerResult{}, nil
	}
	preemptedJobs := make([]*jobdb.Job, 0, len(t.jobsToPreempt))
	scheduledJobs := make([]*jobdb.Job, 0, len(t.jobsToSchedule))
	for _, id := range t.jobsToPreempt {
		job := txn.GetById(id)
		if job == nil {
			return nil, errors.Errorf("was asked to preempt job %s but job does not exist", id)
		}
		if job.InTerminalState() {
			return nil, errors.Errorf("was asked to preempt job %s but job is in terminal state", id)
		}
		if job.Queued() {
			return nil, errors.Errorf("was asked to preempt job %s but job is still queued", job.Id())
		}
		if run := job.LatestRun(); run != nil {
			job = job.WithUpdatedRun(run.WithFailed(true))
		} else {
			return nil, errors.Errorf("attempting to preempt job %s with no associated runs", job.Id())
		}
		job = job.WithQueued(false).WithFailed(true)
		preemptedJobs = append(preemptedJobs, job)
	}
	for _, id := range t.jobsToSchedule {
		job := txn.GetById(id)
		if job == nil {
			return nil, errors.Errorf("was asked to lease %s but job does not exist", id)
		}
		if !job.Queued() {
			return nil, errors.Errorf("was asked to lease %s but job is not queued", job.Id())
		}
		job = job.WithQueuedVersion(job.QueuedVersion()+1).WithQueued(false).WithNewRun(
			testExecutor,
			testNodeId,
			testNode,
			testPool,
			job.PriorityClass().Priority,
		)
		scheduledJobs = append(scheduledJobs, job)
	}
	if err := txn.Upsert(preemptedJobs); err != nil {
		return nil, err
	}
	if err := txn.Upsert(scheduledJobs); err != nil {
		return nil, err
	}
	return NewSchedulerResultForTest(preemptedJobs, scheduledJobs, nil), nil
}

func (t *testSchedulingAlgo) Persist() {
	if t.numberOfScheduleCalls == 0 {
		// Nothing to persist if there have been no calls to schedule.
		return
	}
	t.persisted = true
	return
}

func NewSchedulerResultForTest[S ~[]T, T *jobdb.Job](
	preemptedJobs S,
	scheduledJobs S,
	nodeIdByJobId map[string]string,
) *scheduling.SchedulerResult {
	return &scheduling.SchedulerResult{
		PreemptedJobs: schedulercontext.JobSchedulingContextsFromJobs(preemptedJobs),
		ScheduledJobs: schedulercontext.JobSchedulingContextsFromJobs(scheduledJobs),
		NodeIdByJobId: nodeIdByJobId,
	}
}

type testBidPriceProvider struct {
	numberOfCalls int
	pools         []string
	queues        []string
	priceFunc     func(band bidstore.PriceBand) float64
}

func (t *testBidPriceProvider) GetBidPrices(ctx *armadacontext.Context) (pricing.BidPriceSnapshot, error) {
	t.numberOfCalls++
	snapshot := pricing.BidPriceSnapshot{
		Timestamp: time.Now(),
		Bids:      make(map[pricing.PriceKey]map[string]pricing.Bid),
	}

	for _, q := range t.queues {
		for _, band := range bidstore.PriceBandFromShortName {
			key := pricing.PriceKey{Queue: q, Band: band}
			bids := make(map[string]pricing.Bid)

			for _, pool := range t.pools {
				bids[pool] = pricing.Bid{
					QueuedBid:  t.priceFunc(band),
					RunningBid: t.priceFunc(band),
				}
			}

			snapshot.Bids[key] = bids
		}
	}
	return snapshot, nil
}

type testPublisher struct {
	i              int
	eventSequences []*armadaevents.EventSequence
	shouldError    bool
}

func newTestPublisher() *testPublisher {
	// Initialise with an empty slice to make require.Equal comparison with an initialised slice work.
	return &testPublisher{eventSequences: make([]*armadaevents.EventSequence, 0)}
}

func (t *testPublisher) PublishMessages(_ *armadacontext.Context, events []*armadaevents.EventSequence, shouldPublish func() bool) error {
	if t.shouldError {
		return errors.New("testPublisher error")
	}
	if shouldPublish() {
		t.eventSequences = append(t.eventSequences, events...)
	}
	return nil
}

func (t *testPublisher) ReadAll() []*armadaevents.EventSequence {
	if len(t.eventSequences) == 0 {
		return nil
	}
	rv := t.eventSequences[t.i:len(t.eventSequences)]
	t.i += len(rv)
	return rv
}

func (t *testPublisher) Reset() {
	t.eventSequences = nil
}

func (t *testPublisher) PublishMarkers(ctx *armadacontext.Context, groupId uuid.UUID) (uint32, error) {
	return 100, nil
}

func stringSet(src []string) map[string]bool {
	set := make(map[string]bool, len(src))
	for _, s := range src {
		set[s] = true
	}
	return set
}

var (
	queuedJobA = &database.Job{
		JobID:                 util.NewULID(),
		JobSet:                "testJobSet",
		Queue:                 "testQueue",
		Queued:                true,
		QueuedVersion:         0,
		SchedulingInfo:        schedulingInfoBytes,
		SchedulingInfoVersion: int32(schedulingInfo.Version),
		Validated:             true,
		Serial:                0,
	}
	queuedJobWithFailFastA = &database.Job{
		JobID:                 queuedJobA.JobID,
		JobSet:                "testJobSet",
		Queue:                 "testQueue",
		Queued:                true,
		QueuedVersion:         0,
		SchedulingInfo:        failFastSchedulingInfoBytes,
		SchedulingInfoVersion: int32(failFastSchedulingInfo.Version),
		Validated:             true,
		Serial:                0,
	}
	queuedJobWithUpdatedPriorityA = &database.Job{
		JobID:                 queuedJobA.JobID,
		JobSet:                "testJobSet",
		Queue:                 "testQueue",
		Queued:                true,
		QueuedVersion:         0,
		SchedulingInfo:        schedulingInfoWithUpdatedPriorityBytes,
		SchedulingInfoVersion: int32(schedulingInfoWithUpdatedPriority.Version),
		Validated:             true,
		Serial:                1,
	}
	runningJobA = &database.Job{
		JobID:                 queuedJobA.JobID,
		JobSet:                "testJobSet",
		Queue:                 "testQueue",
		QueuedVersion:         1,
		SchedulingInfo:        schedulingInfoBytes,
		SchedulingInfoVersion: int32(schedulingInfo.Version),
		Validated:             true,
		Serial:                0,
	}
	failedJobA = &database.Job{
		JobID:                 queuedJobA.JobID,
		JobSet:                "testJobSet",
		Queue:                 "testQueue",
		Failed:                true,
		QueuedVersion:         0,
		SchedulingInfo:        schedulingInfoBytes,
		SchedulingInfoVersion: int32(schedulingInfo.Version),
		Validated:             true,
		Serial:                0,
	}
	cancelledJobA = &database.Job{
		JobID:                 queuedJobA.JobID,
		JobSet:                "testJobSet",
		Queue:                 "testQueue",
		Cancelled:             true,
		QueuedVersion:         0,
		SchedulingInfo:        schedulingInfoBytes,
		SchedulingInfoVersion: int32(schedulingInfo.Version),
		Validated:             true,
		Serial:                0,
	}
	cancelRequestedJobA = &database.Job{
		JobID:                 queuedJobA.JobID,
		JobSet:                "testJobSet",
		Queue:                 "testQueue",
		Queued:                true,
		CancelRequested:       true,
		QueuedVersion:         0,
		SchedulingInfo:        schedulingInfoBytes,
		SchedulingInfoVersion: int32(schedulingInfo.Version),
		Validated:             true,
		Serial:                0,
	}
	cancelByJobSetRequestedJobA = &database.Job{
		JobID:                   queuedJobA.JobID,
		JobSet:                  "testJobSet",
		Queue:                   "testQueue",
		Queued:                  true,
		CancelByJobsetRequested: true,
		QueuedVersion:           0,
		SchedulingInfo:          schedulingInfoBytes,
		SchedulingInfoVersion:   int32(schedulingInfo.Version),
		Validated:               true,
		Serial:                  0,
	}
	runningCancelRequestedJobA = &database.Job{
		JobID:                 queuedJobA.JobID,
		JobSet:                "testJobSet",
		Queue:                 "testQueue",
		Queued:                false,
		CancelRequested:       true,
		QueuedVersion:         1,
		SchedulingInfo:        schedulingInfoBytes,
		SchedulingInfoVersion: int32(schedulingInfo.Version),
		Validated:             true,
		Serial:                0,
	}
	runningCancelByJobSetRequestedJobA = &database.Job{
		JobID:                   queuedJobA.JobID,
		JobSet:                  "testJobSet",
		Queue:                   "testQueue",
		CancelByJobsetRequested: true,
		QueuedVersion:           1,
		SchedulingInfo:          schedulingInfoBytes,
		SchedulingInfoVersion:   int32(schedulingInfo.Version),
		Validated:               true,
		Serial:                  0,
	}
	newRunA = &database.Run{
		RunID:               testfixtures.UUIDFromInt(1).String(),
		JobID:               queuedJobA.JobID,
		JobSet:              queuedJobA.JobSet,
		Executor:            testExecutor,
		Node:                testNode,
		RunAttempted:        true,
		Serial:              0,
		ScheduledAtPriority: &scheduledAtPriority,
	}
	successfulRunA = &database.Run{
		RunID:               testfixtures.UUIDFromInt(1).String(),
		JobID:               queuedJobA.JobID,
		JobSet:              queuedJobA.JobSet,
		Executor:            testExecutor,
		Node:                testNode,
		Succeeded:           true,
		RunAttempted:        true,
		Serial:              0,
		ScheduledAtPriority: &scheduledAtPriority,
	}
	failedAttemptedRunA = &database.Run{
		RunID:               testfixtures.UUIDFromInt(1).String(),
		JobID:               queuedJobA.JobID,
		JobSet:              queuedJobA.JobSet,
		Executor:            testExecutor,
		Node:                testNode,
		Failed:              true,
		RunAttempted:        true,
		Serial:              0,
		ScheduledAtPriority: &scheduledAtPriority,
	}
	failedRunA = &database.Run{
		RunID:               testfixtures.UUIDFromInt(1).String(),
		JobID:               queuedJobA.JobID,
		JobSet:              queuedJobA.JobSet,
		Executor:            testExecutor,
		Node:                testNode,
		Failed:              true,
		Serial:              0,
		ScheduledAtPriority: &scheduledAtPriority,
	}
	failedReturnedRunA = &database.Run{
		RunID:               testfixtures.UUIDFromInt(1).String(),
		JobID:               queuedJobA.JobID,
		JobSet:              queuedJobA.JobSet,
		Executor:            testExecutor,
		Node:                testNode,
		Failed:              true,
		Returned:            true,
		Serial:              0,
		ScheduledAtPriority: &scheduledAtPriority,
	}
)

func jobDbJobFromDbJob(resourceListFactory *internaltypes.ResourceListFactory, job *database.Job) *jobdb.Job {
	var schedulingInfo schedulerobjects.JobSchedulingInfo
	protoutil.MustUnmarshall(job.SchedulingInfo, &schedulingInfo)
	// Use a fresh jobDb instance to ensure run ids are consistent.
	result, err := testfixtures.NewJobDb(resourceListFactory).NewJob(
		job.JobID,
		job.JobSet,
		job.Queue,
		uint32(job.Priority),
		toInternalSchedulingInfo(&schedulingInfo),
		job.Queued,
		job.QueuedVersion,
		job.CancelRequested,
		job.CancelByJobsetRequested,
		job.Cancelled,
		0,
		job.Validated,
		job.Pools,
		0,
	)
	if err != nil {
		panic(err)
	}
	return result
}

// TestCycleConsistency runs two replicas of the scheduler and asserts that their state remains consistent
// under various permutations of making scheduling decisions and failing over.
//
// TODO(albin): Test lease expiry.
func TestCycleConsistency(t *testing.T) {
	resourceListFactory := testfixtures.MakeTestResourceListFactory()
	type schedulerDbUpdate struct {
		jobUpdates   []*database.Job              // Job updates from the database.
		runUpdates   []*database.Run              // Run updates from the database.
		jobRunErrors []*armadaevents.JobRunErrors // Job run errors from the database.
	}
	tests := map[string]struct {
		// Each test case consists of two updates written to the scheduler postgres.
		firstSchedulerDbUpdate  schedulerDbUpdate
		secondSchedulerDbUpdate schedulerDbUpdate

		// Inputs of the mocked scheduling algorithm.
		// Controls which jobs the scheduler should schedule/preempt/fail.
		idsOfJobsToSchedule []string
		idsOfJobsToPreempt  []string

		// Expected jobDbs for scenario 1, i.e., the baseline scenario.
		// Only compared against if not nil.
		expectedJobDbCycleOne   []*jobdb.Job
		expectedJobDbCycleTwo   []*jobdb.Job
		expectedJobDbCycleThree []*jobdb.Job

		// Expected published events for scenario 1, i.e., the baseline scenario.
		// Only compared against if not nil.
		expectedEventSequencesCycleOne   []*armadaevents.EventSequence
		expectedEventSequencesCycleTwo   []*armadaevents.EventSequence
		expectedEventSequencesCycleThree []*armadaevents.EventSequence

		// If true then will jobs will fail submit check
		failSubmitCheck bool
	}{
		"Load a queued job": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					queuedJobA,
				},
			},
			expectedJobDbCycleOne: []*jobdb.Job{
				jobDbJobFromDbJob(resourceListFactory, queuedJobA),
			},
			expectedJobDbCycleTwo: []*jobdb.Job{
				jobDbJobFromDbJob(resourceListFactory, queuedJobA),
			},
			expectedJobDbCycleThree: []*jobdb.Job{
				jobDbJobFromDbJob(resourceListFactory, queuedJobA),
			},
			expectedEventSequencesCycleThree: make([]*armadaevents.EventSequence, 0),
		},
		"Load a failed job": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					failedJobA,
				},
			},
			expectedJobDbCycleOne:            []*jobdb.Job{},
			expectedJobDbCycleTwo:            []*jobdb.Job{},
			expectedJobDbCycleThree:          []*jobdb.Job{},
			expectedEventSequencesCycleThree: make([]*armadaevents.EventSequence, 0),
		},
		"Load a cancelled job": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					cancelledJobA,
				},
			},
			expectedJobDbCycleOne:            []*jobdb.Job{},
			expectedJobDbCycleTwo:            []*jobdb.Job{},
			expectedJobDbCycleThree:          []*jobdb.Job{},
			expectedEventSequencesCycleThree: make([]*armadaevents.EventSequence, 0),
		},
		"Load a queued job with cancel requested": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					cancelRequestedJobA,
				},
			},
			expectedJobDbCycleOne: []*jobdb.Job{
				jobDbJobFromDbJob(resourceListFactory, cancelRequestedJobA).WithQueued(false).WithCancelled(true),
			},
			expectedJobDbCycleTwo:   []*jobdb.Job{},
			expectedJobDbCycleThree: []*jobdb.Job{},
			expectedEventSequencesCycleThree: []*armadaevents.EventSequence{
				{
					Queue:      queuedJobA.Queue,
					JobSetName: queuedJobA.JobSet,
					Events: []*armadaevents.EventSequence_Event{
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_CancelledJob{
								CancelledJob: &armadaevents.CancelledJob{
									JobId: queuedJobA.JobID,
								},
							},
						},
					},
				},
			},
		},
		"Load a queued job with cancel by job set requested": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					cancelByJobSetRequestedJobA,
				},
			},
			expectedJobDbCycleOne: []*jobdb.Job{
				jobDbJobFromDbJob(resourceListFactory, cancelByJobSetRequestedJobA).WithQueued(false).WithCancelled(true),
			},
			expectedJobDbCycleTwo:   []*jobdb.Job{},
			expectedJobDbCycleThree: []*jobdb.Job{},
			expectedEventSequencesCycleThree: []*armadaevents.EventSequence{
				{
					Queue:      queuedJobA.Queue,
					JobSetName: queuedJobA.JobSet,
					Events: []*armadaevents.EventSequence_Event{
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_CancelJob{
								CancelJob: &armadaevents.CancelJob{
									JobId: queuedJobA.JobID,
								},
							},
						},
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_CancelledJob{
								CancelledJob: &armadaevents.CancelledJob{
									JobId: queuedJobA.JobID,
								},
							},
						},
					},
				},
			},
		},
		"Load a running job with cancel requested": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					runningCancelRequestedJobA,
				},
				runUpdates: []*database.Run{
					newRunA,
				},
			},
			expectedJobDbCycleOne: []*jobdb.Job{
				func() *jobdb.Job {
					job := jobDbJobFromDbJob(resourceListFactory, runningCancelRequestedJobA).WithCancelled(true).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2)
					return job.WithUpdatedRun(job.LatestRun().WithCancelled(true).WithAttempted(true))
				}(),
			},
			expectedJobDbCycleTwo:   []*jobdb.Job{},
			expectedJobDbCycleThree: []*jobdb.Job{},
			expectedEventSequencesCycleThree: []*armadaevents.EventSequence{
				{
					Queue:      queuedJobA.Queue,
					JobSetName: queuedJobA.JobSet,
					Events: []*armadaevents.EventSequence_Event{
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_JobRunCancelled{
								JobRunCancelled: &armadaevents.JobRunCancelled{
									RunId: testfixtures.UUIDFromInt(1).String(),
									JobId: queuedJobA.JobID,
								},
							},
						},
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_CancelledJob{
								CancelledJob: &armadaevents.CancelledJob{
									JobId: queuedJobA.JobID,
								},
							},
						},
					},
				},
			},
		},
		"Load a running job with cancel by job set requested": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					runningCancelByJobSetRequestedJobA,
				},
				runUpdates: []*database.Run{
					newRunA,
				},
			},
			expectedJobDbCycleOne: []*jobdb.Job{
				func() *jobdb.Job {
					job := jobDbJobFromDbJob(resourceListFactory, runningCancelByJobSetRequestedJobA).WithCancelled(true).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2)
					return job.WithUpdatedRun(job.LatestRun().WithCancelled(true).WithAttempted(true))
				}(),
			},
			expectedJobDbCycleTwo:   []*jobdb.Job{},
			expectedJobDbCycleThree: []*jobdb.Job{},
			expectedEventSequencesCycleThree: []*armadaevents.EventSequence{
				{
					Queue:      queuedJobA.Queue,
					JobSetName: queuedJobA.JobSet,
					Events: []*armadaevents.EventSequence_Event{
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_CancelJob{
								CancelJob: &armadaevents.CancelJob{
									JobId: queuedJobA.JobID,
								},
							},
						},
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_JobRunCancelled{
								JobRunCancelled: &armadaevents.JobRunCancelled{
									JobId: queuedJobA.JobID,
									RunId: testfixtures.UUIDFromInt(1).String(),
								},
							},
						},
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_CancelledJob{
								CancelledJob: &armadaevents.CancelledJob{
									JobId: queuedJobA.JobID,
								},
							},
						},
					},
				},
			},
		},
		"Schedule a new job": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					queuedJobA,
				},
			},
			idsOfJobsToSchedule: []string{queuedJobA.JobID},
			expectedJobDbCycleOne: []*jobdb.Job{
				jobDbJobFromDbJob(resourceListFactory, queuedJobA).WithQueued(false).WithQueuedVersion(1).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2),
			},
			expectedJobDbCycleTwo: []*jobdb.Job{
				jobDbJobFromDbJob(resourceListFactory, queuedJobA).WithQueued(false).WithQueuedVersion(1).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2),
			},
			expectedJobDbCycleThree: []*jobdb.Job{
				jobDbJobFromDbJob(resourceListFactory, queuedJobA).WithQueued(false).WithQueuedVersion(1).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2),
			},
			expectedEventSequencesCycleThree: []*armadaevents.EventSequence{
				{
					Queue:      queuedJobA.Queue,
					JobSetName: queuedJobA.JobSet,
					Events: []*armadaevents.EventSequence_Event{
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_JobRunLeased{
								JobRunLeased: &armadaevents.JobRunLeased{
									RunId:                  testfixtures.UUIDFromInt(1).String(),
									JobId:                  queuedJobA.JobID,
									ExecutorId:             testExecutor,
									NodeId:                 testNode,
									UpdateSequenceNumber:   1,
									HasScheduledAtPriority: true,
									ScheduledAtPriority:    2,
									PodRequirementsOverlay: &schedulerobjects.PodRequirements{},
									Pool:                   testPool,
								},
							},
						},
					},
				},
			},
		},
		"Schedule a job that then succeeds": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					queuedJobA,
				},
			},
			secondSchedulerDbUpdate: schedulerDbUpdate{
				runUpdates: []*database.Run{
					successfulRunA,
				},
			},
			idsOfJobsToSchedule: []string{queuedJobA.JobID},
			expectedJobDbCycleOne: []*jobdb.Job{
				jobDbJobFromDbJob(resourceListFactory, queuedJobA).WithQueued(false).WithQueuedVersion(1).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2),
			},
			expectedJobDbCycleTwo: []*jobdb.Job{
				func() *jobdb.Job {
					job := jobDbJobFromDbJob(resourceListFactory, queuedJobA).WithQueued(false).WithQueuedVersion(1).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2).WithSucceeded(true)
					return job.WithUpdatedRun(job.LatestRun().WithSucceeded(true).WithAttempted(true))
				}(),
			},
			expectedJobDbCycleThree: make([]*jobdb.Job, 0),
			expectedEventSequencesCycleThree: []*armadaevents.EventSequence{
				{
					Queue:      queuedJobA.Queue,
					JobSetName: queuedJobA.JobSet,
					Events: []*armadaevents.EventSequence_Event{
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_JobRunLeased{
								JobRunLeased: &armadaevents.JobRunLeased{
									JobId:                  queuedJobA.JobID,
									RunId:                  testfixtures.UUIDFromInt(1).String(),
									ExecutorId:             testExecutor,
									NodeId:                 testNode,
									UpdateSequenceNumber:   1,
									HasScheduledAtPriority: true,
									ScheduledAtPriority:    2,
									PodRequirementsOverlay: &schedulerobjects.PodRequirements{},
									Pool:                   testPool,
								},
							},
						},
					},
				},
				{
					Queue:      queuedJobA.Queue,
					JobSetName: queuedJobA.JobSet,
					Events: []*armadaevents.EventSequence_Event{
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_JobSucceeded{
								JobSucceeded: &armadaevents.JobSucceeded{
									JobId: queuedJobA.JobID,
								},
							},
						},
					},
				},
			},
		},
		"Schedule a new job that then fails after starting to run": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					queuedJobA,
				},
			},
			secondSchedulerDbUpdate: schedulerDbUpdate{
				runUpdates: []*database.Run{
					failedAttemptedRunA,
				},
				jobRunErrors: []*armadaevents.JobRunErrors{
					defaultJobRunError(queuedJobA.JobID, failedAttemptedRunA.RunID),
				},
			},
			idsOfJobsToSchedule: []string{queuedJobA.JobID},
			expectedJobDbCycleOne: []*jobdb.Job{
				jobDbJobFromDbJob(resourceListFactory, queuedJobA).WithQueued(false).WithQueuedVersion(1).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2),
			},
			expectedJobDbCycleTwo: []*jobdb.Job{
				func() *jobdb.Job {
					job := jobDbJobFromDbJob(resourceListFactory, queuedJobA).WithQueued(false).WithQueuedVersion(1).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2).WithFailed(true)
					return job.WithUpdatedRun(job.LatestRun().WithFailed(true).WithAttempted(true))
				}(),
			},
			expectedJobDbCycleThree: make([]*jobdb.Job, 0),
			expectedEventSequencesCycleThree: []*armadaevents.EventSequence{
				{
					Queue:      queuedJobA.Queue,
					JobSetName: queuedJobA.JobSet,
					Events: []*armadaevents.EventSequence_Event{
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_JobRunLeased{
								JobRunLeased: &armadaevents.JobRunLeased{
									JobId:                  queuedJobA.JobID,
									RunId:                  testfixtures.UUIDFromInt(1).String(),
									ExecutorId:             testExecutor,
									NodeId:                 testNode,
									UpdateSequenceNumber:   1,
									HasScheduledAtPriority: true,
									ScheduledAtPriority:    2,
									PodRequirementsOverlay: &schedulerobjects.PodRequirements{},
									Pool:                   testPool,
								},
							},
						},
					},
				},
				{
					Queue:      queuedJobA.Queue,
					JobSetName: queuedJobA.JobSet,
					Events: []*armadaevents.EventSequence_Event{
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_JobErrors{
								JobErrors: &armadaevents.JobErrors{
									JobId: queuedJobA.JobID,
									Errors: []*armadaevents.Error{
										{
											Terminal: true,
											Reason: &armadaevents.Error_PodError{
												PodError: &armadaevents.PodError{
													Message: "generic pod error",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		"Schedule a new job that then fails to start": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					queuedJobA,
				},
			},
			secondSchedulerDbUpdate: schedulerDbUpdate{
				runUpdates: []*database.Run{
					failedRunA,
				},
				jobRunErrors: []*armadaevents.JobRunErrors{
					defaultJobRunError(queuedJobA.JobID, failedAttemptedRunA.RunID),
				},
			},
			idsOfJobsToSchedule: []string{queuedJobA.JobID},
			expectedJobDbCycleOne: []*jobdb.Job{
				jobDbJobFromDbJob(resourceListFactory, queuedJobA).WithQueued(false).WithQueuedVersion(1).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2),
			},
			expectedJobDbCycleTwo: []*jobdb.Job{
				func() *jobdb.Job {
					job := jobDbJobFromDbJob(resourceListFactory, queuedJobA).WithQueued(false).WithQueuedVersion(1).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2).WithFailed(true)
					// TODO(albin): RunAttempted is implicitly set to true for failed runs with error other than PodLeaseReturned.
					//              See func (c *JobSetEventsInstructionConverter) handleJobRunErrors.
					return job.WithUpdatedRun(job.LatestRun().WithFailed(true).WithAttempted(true))
				}(),
			},
			expectedJobDbCycleThree: []*jobdb.Job{},
		},
		"Schedule a new job that then fails to start and is returned": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					queuedJobA,
				},
			},
			secondSchedulerDbUpdate: schedulerDbUpdate{
				runUpdates: []*database.Run{
					failedReturnedRunA,
				},
				jobRunErrors: []*armadaevents.JobRunErrors{
					defaultJobRunError(queuedJobA.JobID, failedAttemptedRunA.RunID),
				},
			},
			idsOfJobsToSchedule: []string{queuedJobA.JobID},
			expectedJobDbCycleOne: []*jobdb.Job{
				jobDbJobFromDbJob(resourceListFactory, queuedJobA).WithQueued(false).WithQueuedVersion(1).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2),
			},
			expectedJobDbCycleTwo: []*jobdb.Job{
				func() *jobdb.Job {
					job := jobDbJobFromDbJob(resourceListFactory, queuedJobA).WithQueued(false).WithQueuedVersion(1).WithNewRun(testExecutor, testNodeId, testNode, testPool, 2).WithFailed(true)
					return job.WithUpdatedRun(job.LatestRun().WithFailed(true).WithAttempted(true).WithReturned(true))
				}(),
			},
			expectedJobDbCycleThree: []*jobdb.Job{},
			failSubmitCheck:         true,
		},
		"Schedule a new job that then fails to start with fail-fast set to true": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					queuedJobWithFailFastA,
				},
			},
			secondSchedulerDbUpdate: schedulerDbUpdate{
				runUpdates: []*database.Run{
					failedRunA,
				},
				jobRunErrors: []*armadaevents.JobRunErrors{
					defaultJobRunError(queuedJobA.JobID, failedAttemptedRunA.RunID),
				},
			},
			idsOfJobsToSchedule: []string{queuedJobA.JobID},
		},
		// TODO(albin): Also test a job being attempted to many times.
		"Running job is preempted": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					runningJobA,
				},
				runUpdates: []*database.Run{
					newRunA,
				},
			},
			idsOfJobsToPreempt:      []string{queuedJobA.JobID},
			expectedJobDbCycleThree: make([]*jobdb.Job, 0),
			expectedEventSequencesCycleThree: []*armadaevents.EventSequence{
				{
					Queue:      queuedJobA.Queue,
					JobSetName: queuedJobA.JobSet,
					Events: []*armadaevents.EventSequence_Event{
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_JobRunPreempted{
								JobRunPreempted: &armadaevents.JobRunPreempted{
									PreemptedRunId: testfixtures.UUIDFromInt(1).String(),
									PreemptedJobId: queuedJobA.JobID,
								},
							},
						},
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_JobRunErrors{
								JobRunErrors: &armadaevents.JobRunErrors{
									JobId: queuedJobA.JobID,
									RunId: testfixtures.UUIDFromInt(1).String(),
									Errors: []*armadaevents.Error{
										{
											Terminal: true,
											Reason: &armadaevents.Error_JobRunPreemptedError{
												JobRunPreemptedError: &armadaevents.JobRunPreemptedError{},
											},
										},
									},
								},
							},
						},
						{
							Created: &types.Timestamp{},
							Event: &armadaevents.EventSequence_Event_JobErrors{
								JobErrors: &armadaevents.JobErrors{
									JobId: queuedJobA.JobID,
									Errors: []*armadaevents.Error{
										{
											Terminal: true,
											Reason: &armadaevents.Error_JobRunPreemptedError{
												JobRunPreemptedError: &armadaevents.JobRunPreemptedError{},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		"Queued job is cancelled": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					queuedJobA,
				},
			},
			secondSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					cancelRequestedJobA,
				},
			},
		},
		"Running job is cancelled": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					runningJobA,
				},
				runUpdates: []*database.Run{
					newRunA,
				},
			},
			secondSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					runningCancelRequestedJobA,
				},
			},
		},
		"Queued job is cancelled by job set": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					queuedJobA,
				},
			},
			secondSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					cancelByJobSetRequestedJobA,
				},
			},
		},
		"Running job is cancelled by job set": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					runningJobA,
				},
				runUpdates: []*database.Run{
					newRunA,
				},
			},
			secondSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					runningCancelByJobSetRequestedJobA,
				},
			},
		},
		"Queued job is re-prioritised": {
			firstSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					queuedJobA,
				},
			},
			secondSchedulerDbUpdate: schedulerDbUpdate{
				jobUpdates: []*database.Job{
					queuedJobWithUpdatedPriorityA,
				},
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := armadacontext.Background()
			testClock := clock.NewFakeClock(time.Unix(0, 0))

			// Setup necessary for creating schedulerDb operations.
			queueByJobId := make(map[string]string)
			jobSetByJobId := make(map[string]string)
			for _, jobUpdate := range tc.firstSchedulerDbUpdate.jobUpdates {
				queueByJobId[jobUpdate.JobID] = jobUpdate.Queue
				jobSetByJobId[jobUpdate.JobID] = jobUpdate.JobSet
			}
			for _, jobUpdate := range tc.secondSchedulerDbUpdate.jobUpdates {
				queueByJobId[jobUpdate.JobID] = jobUpdate.Queue
				jobSetByJobId[jobUpdate.JobID] = jobUpdate.JobSet
			}
			instructionConverter, err := scheduleringester.NewJobSetEventsInstructionConverter(nil)
			require.NoError(t, err)

			// Helper function for creating new schedulers for use in tests.
			newScheduler := func(db *pgxpool.Pool) *Scheduler {
				scheduler, err := NewScheduler(
					testfixtures.NewJobDb(resourceListFactory),
					database.NewPostgresJobRepository(db, 1024),
					&testExecutorRepository{
						updateTimes: map[string]time.Time{"test-executor": testClock.Now()},
					},
					&testSchedulingAlgo{
						jobsToSchedule: tc.idsOfJobsToSchedule,
						jobsToPreempt:  tc.idsOfJobsToPreempt,
					},
					leader.NewStandaloneLeaderController(),
					newTestPublisher(),
					&testSubmitChecker{
						checkSuccess: !tc.failSubmitCheck,
					},
					1*time.Second,
					5*time.Second,
					0,
					nil,
					maxNumberOfAttempts,
					nodeIdLabel,
					schedulerMetrics,
					pricing.NoopBidPriceProvider{},
					[]string{},
				)
				require.NoError(t, err)
				scheduler.clock = testClock
				scheduler.EnableAssertions()
				return scheduler
			}

			withTestSetup := func(f func(a, b *Scheduler, schedulerDb *scheduleringester.SchedulerDb) error) error {
				return schedulerdb.WithTestDb(func(_ *schedulerdb.Queries, db *pgxpool.Pool) error {
					// Create a schedulerDb using this db connection and initialise its state.
					schedulerDb := scheduleringester.NewSchedulerDb(
						db,
						nil,
						time.Second,
						time.Second,
						10*time.Second,
					)

					// Create two scheduler using the same db connection.
					a := newScheduler(db)
					b := newScheduler(db)

					// Share the schedulingAlgo to ensure both schedulers make the same scheduling decisions.
					b.schedulingAlgo = a.schedulingAlgo

					// Initially, "a" is leader and "b" follower.
					(b.leaderController.(*leader.StandaloneLeaderController)).SetToken(leader.InvalidLeaderToken())

					return f(a, b, schedulerDb)
				})
			}

			// Helper function for {first,second}DbUpdate.
			dbUpdate := func(
				schedulerDb *scheduleringester.SchedulerDb,
				jobUpdates []*database.Job,
				runUpdates []*database.Run,
				jobRunErrors []*armadaevents.JobRunErrors,
			) error {
				dbOps, err := dbOpsFromDbObjects(
					ctx,
					instructionConverter,
					queueByJobId,
					jobSetByJobId,
					jobUpdates,
					runUpdates,
					jobRunErrors,
				)
				if err != nil {
					return err
				}
				return schedulerDb.Store(ctx, &scheduleringester.DbOperationsWithMessageIds{Ops: dbOps})
			}

			// write the first schedulerDb update to postgres.
			firstDbUpdate := func(schedulerDb *scheduleringester.SchedulerDb) error {
				t.Log("writing first schedulerDb update")
				return dbUpdate(
					schedulerDb,
					tc.firstSchedulerDbUpdate.jobUpdates,
					tc.firstSchedulerDbUpdate.runUpdates,
					tc.firstSchedulerDbUpdate.jobRunErrors,
				)
			}

			// write the second schedulerDb update to postgres.
			secondDbUpdate := func(schedulerDb *scheduleringester.SchedulerDb) error {
				t.Log("writing second schedulerDb update")
				return dbUpdate(
					schedulerDb,
					tc.secondSchedulerDbUpdate.jobUpdates,
					tc.secondSchedulerDbUpdate.runUpdates,
					tc.secondSchedulerDbUpdate.jobRunErrors,
				)
			}

			// cycle runs one cycle.
			cycle := func(s *Scheduler, updateAll, shouldSchedule bool) error {
				t.Logf("cycle scheduler %p", s)
				_, err := s.cycle(ctx, updateAll, s.leaderController.GetToken(), shouldSchedule)
				return err
			}

			// persist persists to the schedulerDb any published eventSequences.
			persist := func(s *Scheduler, schedulerDb *scheduleringester.SchedulerDb) error {
				publisher := s.publisher.(*testPublisher)
				events := publisher.ReadAll()
				sequences := &utils.EventsWithIds[*armadaevents.EventSequence]{
					Events: events,
				}
				dbOpsWithMessageIds := instructionConverter.Convert(ctx, sequences)

				// Mark scheduling decisions as persisted.
				// If not persisted, the same jobs are scheduled again on subsequent calls to schedule.
				s.schedulingAlgo.(*testSchedulingAlgo).Persist()

				// Logging
				numEvents := 0
				for _, eventSequence := range sequences.Events {
					numEvents += len(eventSequence.Events)
				}
				t.Logf("persist scheduler %p; %d eventSequences, %d eventSequences, %d ops", s, len(sequences.Events), numEvents, len(dbOpsWithMessageIds.Ops))
				for _, eventSequence := range sequences.Events {
					for _, event := range eventSequence.Events {
						t.Logf("\tevent %s, %s, %T", eventSequence.Queue, eventSequence.JobSetName, event.Event)
					}
				}
				for _, op := range dbOpsWithMessageIds.Ops {
					t.Logf("\toperation %T: %v", op, op)
				}

				return schedulerDb.Store(ctx, dbOpsWithMessageIds)
			}

			// failover swaps the leader tokens between a and b, thus swapping which scheduler is leader.
			failover := func(a, b *Scheduler) error {
				t.Logf("failover schedulers %p, %p", a, b)
				lca := a.leaderController.(*leader.StandaloneLeaderController)
				lcb := b.leaderController.(*leader.StandaloneLeaderController)
				lta := lca.GetToken()
				ltb := lcb.GetToken()
				lca.SetToken(ltb)
				lcb.SetToken(lta)
				return nil
			}

			eventsFromTestPublisher := func(p Publisher) []*armadaevents.EventSequence {
				return p.(*testPublisher).eventSequences
			}

			var (
				jobDbCycleOne   *jobdb.JobDb
				jobDbCycleTwo   *jobdb.JobDb
				jobDbCycleThree *jobdb.JobDb

				eventsCycleOne   []*armadaevents.EventSequence
				eventsCycleTwo   []*armadaevents.EventSequence
				eventsCycleThree []*armadaevents.EventSequence
			)

			// One scheduler performing three cycles.
			// Used for absolute assertions and as a baseline for relative assertions.
			t.Log("scenario 1")
			require.NoError(t, withTestSetup(func(a, _ *Scheduler, schedulerDb *scheduleringester.SchedulerDb) error {
				require.NoError(t, firstDbUpdate(schedulerDb))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, persist(a, schedulerDb))
				jobDbCycleOne = a.jobDb.Clone()
				eventsCycleOne = slices.Clone(eventsFromTestPublisher(a.publisher))

				require.NoError(t, secondDbUpdate(schedulerDb))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, persist(a, schedulerDb))
				jobDbCycleTwo = a.jobDb.Clone()
				eventsCycleTwo = slices.Clone(eventsFromTestPublisher(a.publisher))

				require.NoError(t, cycle(a, false, true))
				require.NoError(t, persist(a, schedulerDb))
				jobDbCycleThree = a.jobDb.Clone()
				eventsCycleThree = slices.Clone(eventsFromTestPublisher(a.publisher))

				return nil
			}))

			// Absolute assertions.
			if tc.expectedJobDbCycleOne != nil {
				jobDb := testfixtures.NewJobDbWithJobs(tc.expectedJobDbCycleOne)
				require.NoError(t, jobDb.ReadTxn().AssertEqual(jobDbCycleOne.ReadTxn()), "unexpected cycle one jobDb")
			}
			if tc.expectedJobDbCycleTwo != nil {
				jobDb := testfixtures.NewJobDbWithJobs(tc.expectedJobDbCycleTwo)
				require.NoError(t, jobDb.ReadTxn().AssertEqual(jobDbCycleTwo.ReadTxn()), "unexpected cycle two jobDb")
			}
			if tc.expectedJobDbCycleThree != nil {
				jobDb := testfixtures.NewJobDbWithJobs(tc.expectedJobDbCycleThree)
				require.NoError(t, jobDb.ReadTxn().AssertEqual(jobDbCycleThree.ReadTxn()), "unexpected cycle three jobDb")
			}

			if tc.expectedEventSequencesCycleOne != nil {
				require.Equal(t, tc.expectedEventSequencesCycleOne, eventsCycleOne, "unexpected cycle one events")
			}
			if tc.expectedEventSequencesCycleTwo != nil {
				require.Equal(t, tc.expectedEventSequencesCycleTwo, eventsCycleTwo, "unexpected cycle two events")
			}
			if tc.expectedEventSequencesCycleThree != nil {
				require.Equal(t, tc.expectedEventSequencesCycleThree, eventsCycleThree, "unexpected cycle three events")
			}

			// Test that the follower stays in sync with the leader.
			// TODO(albin): We need to cycle "a" again after each persist for now.
			//              The jobDb of the leader is supposed to be updated immediately to reflect published events.
			//              However, the jobDb is only updated immediately for schedule/preempt/fail decisions.
			//              For external changes, e.g., job succeeded, the jobDb is only updated on the second cycle.
			// TODO(albin): We actually need to fix this since we may preempt an already successful job as it is.
			// TODO(albin): Can we become leader again after failing over to become a follower when leader?
			//              Let's test multiple failovers where you go back and forth.
			t.Log("scenario 2")
			// TODO(albin): Compare a against baseline a.
			require.NoError(t, withTestSetup(func(a, b *Scheduler, schedulerDb *scheduleringester.SchedulerDb) error {
				require.NoError(t, firstDbUpdate(schedulerDb))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, jobDbCycleOne.ReadTxn().AssertEqual(a.jobDb.ReadTxn()))
				require.NoError(t, persist(a, schedulerDb))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, cycle(b, false, false))
				require.NoError(t, a.jobDb.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))
				require.Empty(t, eventsFromTestPublisher(b.publisher))

				require.NoError(t, secondDbUpdate(schedulerDb))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, persist(a, schedulerDb))
				require.NoError(t, jobDbCycleTwo.ReadTxn().AssertEqual(a.jobDb.ReadTxn()))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, cycle(b, false, false))
				require.NoError(t, a.jobDb.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				require.NoError(t, cycle(a, false, true))
				require.NoError(t, persist(a, schedulerDb))
				require.NoError(t, cycle(b, false, false))
				require.NoError(t, a.jobDb.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				return nil
			}))

			// Test that the follower stays in sync with the leader when the follower cycles once before "a" persists.
			t.Log("scenario 3")
			require.NoError(t, withTestSetup(func(a, b *Scheduler, schedulerDb *scheduleringester.SchedulerDb) error {
				require.NoError(t, firstDbUpdate(schedulerDb))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, cycle(b, false, false))
				require.NoError(t, persist(a, schedulerDb))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, cycle(b, false, false))
				require.NoError(t, a.jobDb.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				require.NoError(t, secondDbUpdate(schedulerDb))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, cycle(b, false, false))
				require.NoError(t, persist(a, schedulerDb))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, cycle(b, false, false))
				require.NoError(t, a.jobDb.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				require.NoError(t, cycle(a, false, true))
				require.NoError(t, cycle(b, false, false))
				require.NoError(t, persist(a, schedulerDb))
				require.NoError(t, cycle(b, false, false))
				require.NoError(t, a.jobDb.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				return nil
			}))

			// Test follower failover.
			t.Log("scenario 4")
			require.NoError(t, withTestSetup(func(a, b *Scheduler, schedulerDb *scheduleringester.SchedulerDb) error {
				require.NoError(t, firstDbUpdate(schedulerDb))
				require.NoError(t, cycle(b, false, false))

				require.Empty(t, eventsFromTestPublisher(b.publisher))
				require.NoError(t, failover(a, b))
				require.NoError(t, cycle(b, true, true))
				require.NoError(t, persist(b, schedulerDb))
				require.NoError(t, jobDbCycleOne.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				require.NoError(t, secondDbUpdate(schedulerDb))
				require.NoError(t, cycle(b, false, true))
				require.NoError(t, persist(b, schedulerDb))
				require.NoError(t, jobDbCycleTwo.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				require.NoError(t, cycle(b, false, true))
				require.NoError(t, persist(b, schedulerDb))
				require.NoError(t, jobDbCycleThree.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				return nil
			}))

			// Test follower failover after the first db update.
			t.Log("scenario 5")
			require.NoError(t, withTestSetup(func(a, b *Scheduler, schedulerDb *scheduleringester.SchedulerDb) error {
				require.NoError(t, firstDbUpdate(schedulerDb))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, persist(a, schedulerDb))

				require.NoError(t, secondDbUpdate(schedulerDb))
				require.NoError(t, failover(a, b))
				require.NoError(t, cycle(b, true, true))
				require.NoError(t, persist(b, schedulerDb))
				require.NoError(t, jobDbCycleTwo.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				require.NoError(t, cycle(b, false, true))
				require.NoError(t, persist(b, schedulerDb))
				require.NoError(t, jobDbCycleThree.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				return nil
			}))

			// Test follower failover after the first db update, when the follower has already cycled once.
			t.Log("scenario 6")
			require.NoError(t, withTestSetup(func(a, b *Scheduler, schedulerDb *scheduleringester.SchedulerDb) error {
				require.NoError(t, firstDbUpdate(schedulerDb))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, persist(a, schedulerDb))

				require.NoError(t, secondDbUpdate(schedulerDb))
				require.NoError(t, cycle(b, false, false))
				require.NoError(t, failover(a, b))
				require.NoError(t, cycle(b, true, true))
				require.NoError(t, persist(b, schedulerDb))
				require.NoError(t, jobDbCycleTwo.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				require.NoError(t, cycle(b, false, true))
				require.NoError(t, persist(b, schedulerDb))
				require.NoError(t, jobDbCycleThree.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				return nil
			}))

			// Test follower failover after the first db update, when the follower has already cycled once before the second update.
			t.Log("scenario 7")
			require.NoError(t, withTestSetup(func(a, b *Scheduler, schedulerDb *scheduleringester.SchedulerDb) error {
				require.NoError(t, firstDbUpdate(schedulerDb))
				require.NoError(t, cycle(a, false, true))
				require.NoError(t, persist(a, schedulerDb))

				require.NoError(t, cycle(b, false, false))
				require.NoError(t, secondDbUpdate(schedulerDb))
				require.NoError(t, failover(a, b))
				require.NoError(t, cycle(b, true, true))
				require.NoError(t, persist(b, schedulerDb))
				require.NoError(t, jobDbCycleTwo.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				require.NoError(t, cycle(b, false, true))
				require.NoError(t, persist(b, schedulerDb))
				require.NoError(t, jobDbCycleThree.ReadTxn().AssertEqual(b.jobDb.ReadTxn()))

				return nil
			}))
		})
	}
}

func dbOpsFromDbObjects(
	ctx *armadacontext.Context,
	instructionConverter *scheduleringester.JobSetEventsInstructionConverter,
	queueByJobId map[string]string,
	jobSetByJobId map[string]string,
	jobUpdates []*database.Job,
	runUpdates []*database.Run,
	jobRunErrors []*armadaevents.JobRunErrors,
) ([]scheduleringester.DbOperation, error) {
	dbOps := make([]scheduleringester.DbOperation, 0)

	// jobUpdatesByJobId := make(map[string]*database.Job)
	insertJobsDbOp := make(scheduleringester.InsertJobs, len(jobUpdates))
	for _, dbJob := range jobUpdates {
		insertJobsDbOp[dbJob.JobID] = dbJob
		// jobUpdatesByJobId[dbJob.JobID] = dbJob
	}
	dbOps = scheduleringester.AppendDbOperation(dbOps, fixInsertJobsDbOp(insertJobsDbOp))

	insertRunsDbOp := make(scheduleringester.InsertRuns, len(runUpdates))
	for _, dbRun := range runUpdates {
		queue, ok := queueByJobId[dbRun.JobID]
		if !ok {
			return nil, errors.Errorf("run %s is associated with non-existing job %s", dbRun.RunID, dbRun.JobID)
		}
		insertRunsDbOp[dbRun.RunID] = &scheduleringester.JobRunDetails{
			Queue: queue,
			DbRun: dbRun,
		}
	}
	dbOps = scheduleringester.AppendDbOperation(dbOps, insertRunsDbOp)

	jobRunErrorsEventSequences := make([]*armadaevents.EventSequence, 0, len(jobRunErrors))
	for _, jobRunError := range jobRunErrors {
		jobId := jobRunError.JobId
		queue, ok := queueByJobId[jobId]
		if !ok {
			return nil, errors.Errorf("jobRunError is associated with non-existing job %s", jobId)
		}
		jobSet, ok := jobSetByJobId[jobId]
		if !ok {
			return nil, errors.Errorf("jobRunError is associated with non-existing job %s", jobId)
		}
		eventSequence := &armadaevents.EventSequence{
			Queue:      queue,
			JobSetName: jobSet,
			Events: []*armadaevents.EventSequence_Event{
				{
					Event: &armadaevents.EventSequence_Event_JobRunErrors{JobRunErrors: jobRunError},
				},
			},
		}
		jobRunErrorsEventSequences = append(jobRunErrorsEventSequences, eventSequence)
	}
	insertJobRunErrorsDbOps := instructionConverter.Convert(
		ctx,
		&utils.EventsWithIds[*armadaevents.EventSequence]{
			Events: jobRunErrorsEventSequences,
		},
	)
	for _, dbOp := range insertJobRunErrorsDbOps.Ops {
		dbOps = scheduleringester.AppendDbOperation(dbOps, dbOp)
	}

	return dbOps, nil
}

func fixInsertJobsDbOp(dbOp scheduleringester.InsertJobs) scheduleringester.InsertJobs {
	for _, job := range dbOp {
		// This field must be non-null when written to postgres.
		job.SubmitMessage = make([]byte, 0)
	}
	return dbOp
}

func toInternalSchedulingInfo(j *schedulerobjects.JobSchedulingInfo) *internaltypes.JobSchedulingInfo {
	internalJsi, err := internaltypes.FromSchedulerObjectsJobSchedulingInfo(j)
	if err != nil {
		panic(err)
	}
	return internalJsi
}

func createPreemptibleGangJob() *jobdb.Job {
	return testfixtures.NewJob(
		util.NewULID(),
		"testJobset",
		"testQueue",
		0,
		toInternalSchedulingInfo(preemptibleGangSchedulingInfo),
		false,
		1,
		false,
		false,
		false,
		1,
		true,
	).WithNewRun("testExecutor", "test-node", "node", "pool", 5)
}
