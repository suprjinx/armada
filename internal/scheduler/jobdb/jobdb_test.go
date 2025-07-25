package jobdb

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/armadaproject/armada/internal/common/stringinterner"
	"github.com/armadaproject/armada/internal/common/types"
	"github.com/armadaproject/armada/internal/common/util"
	"github.com/armadaproject/armada/internal/scheduler/internaltypes"
	armadaconfiguration "github.com/armadaproject/armada/internal/server/configuration"
)

func NewTestJobDb() *JobDb {
	return NewJobDb(
		map[string]types.PriorityClass{
			"foo": {},
			"bar": {},
		},
		"foo",
		stringinterner.New(1024),
		testResourceListFactory,
	)
}

func TestJobDb_TestUpsert(t *testing.T) {
	jobDb := NewTestJobDb()

	job1 := newJob()
	job2 := newJob()
	txn := jobDb.WriteTxn()

	// Insert Job
	err := txn.Upsert([]*Job{job1, job2})
	require.NoError(t, err)
	retrieved := txn.GetById(job1.Id())
	assert.Equal(t, job1, retrieved)
	retrieved = txn.GetById(job2.Id())
	assert.Equal(t, job2, retrieved)

	// Updated Job
	job1Updated := job1.WithQueued(true)
	err = txn.Upsert([]*Job{job1Updated})
	require.NoError(t, err)
	retrieved = txn.GetById(job1.Id())
	assert.Equal(t, job1Updated, retrieved)

	// Can't insert with read only transaction
	err = (jobDb.ReadTxn()).Upsert([]*Job{job1})
	require.Error(t, err)
}

func TestJobDb_TestGetById(t *testing.T) {
	jobDb := NewTestJobDb()
	job1 := newJob()
	job2 := newJob()
	txn := jobDb.WriteTxn()

	err := txn.Upsert([]*Job{job1, job2})
	require.NoError(t, err)
	assert.Equal(t, job1, txn.GetById(job1.Id()))
	assert.Equal(t, job2, txn.GetById(job2.Id()))
	assert.Nil(t, txn.GetById(util.NewULID()))
}

func TestJobDb_TestGetUnvalidated(t *testing.T) {
	jobDb := NewTestJobDb()
	job1 := newJob().WithValidated(false)
	job2 := newJob().WithValidated(true)
	job3 := newJob().WithValidated(false)
	txn := jobDb.WriteTxn()

	err := txn.Upsert([]*Job{job1, job2, job3})
	require.NoError(t, err)

	expected := []*Job{job1, job3}

	var actual []*Job
	it := txn.UnvalidatedJobs()
	for job, _ := it.Next(); job != nil; job, _ = it.Next() {
		actual = append(actual, job)
	}
	sort.SliceStable(actual, func(i, j int) bool { return actual[i].id < actual[j].id })
	sort.SliceStable(expected, func(i, j int) bool { return expected[i].id < expected[j].id })
	assert.Equal(t, expected, actual)
}

func TestJobDb_TestGetJobsByGangId(t *testing.T) {
	jobDb := NewTestJobDb()
	job1 := newGangJob()
	job2 := newGangJob()
	txn := jobDb.WriteTxn()

	// Add job1
	err := txn.Upsert([]*Job{job1})
	require.NoError(t, err)
	result := txn.GetGangJobsIdsByGangId(job1.Queue(), job1.GetGangInfo().Id())
	assert.Len(t, result, 1)
	assert.Equal(t, job1.Id(), result[0])

	// Add job2
	err = txn.Upsert([]*Job{job2})
	require.NoError(t, err)
	result = txn.GetGangJobsIdsByGangId(job1.Queue(), job1.GetGangInfo().Id())
	assert.Len(t, result, 2)
	assert.Contains(t, result, job1.Id())
	assert.Contains(t, result, job2.Id())

	// Delete job1
	err = txn.BatchDelete([]string{job1.Id()})
	require.NoError(t, err)
	result = txn.GetGangJobsIdsByGangId(job1.Queue(), job1.GetGangInfo().Id())
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, job2.Id(), result[0])

	// Delete job2
	err = txn.BatchDelete([]string{job2.Id()})
	require.NoError(t, err)
	result = txn.GetGangJobsIdsByGangId(job1.Queue(), job1.GetGangInfo().Id())
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestJobDb_TestGetJobsByGangId_NonGangJob(t *testing.T) {
	jobDb := NewTestJobDb()
	job1 := newJob()
	txn := jobDb.WriteTxn()

	err := txn.Upsert([]*Job{job1})
	require.NoError(t, err)

	result, err := txn.GetGangJobsByGangId(job1.Queue(), job1.GetGangInfo().Id())
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestJobDb_TestGetByRunId(t *testing.T) {
	jobDb := NewTestJobDb()
	job1 := newJob().WithNewRun("executor", "nodeId", "nodeName", "pool", 5)
	job2 := newJob().WithNewRun("executor", "nodeId", "nodeName", "pool", 10)
	txn := jobDb.WriteTxn()

	err := txn.Upsert([]*Job{job1, job2})
	require.NoError(t, err)
	assert.Equal(t, job1, txn.GetByRunId(job1.LatestRun().id))
	assert.Equal(t, job2, txn.GetByRunId(job2.LatestRun().id))
	assert.Nil(t, txn.GetByRunId(uuid.NewString()))

	err = txn.BatchDelete([]string{job1.Id()})
	require.NoError(t, err)
	assert.Nil(t, txn.GetByRunId(job1.LatestRun().id))
}

func TestJobDb_TestHasQueuedJobs(t *testing.T) {
	jobDb := NewTestJobDb()
	job1 := newJob().WithNewRun("executor", "nodeId", "nodeName", "pool", 5)
	job2 := newJob().WithNewRun("executor", "nodeId", "nodeName", "pool", 10)
	txn := jobDb.WriteTxn()

	err := txn.Upsert([]*Job{job1, job2})
	require.NoError(t, err)
	assert.False(t, txn.HasQueuedJobs(job1.queue))
	assert.False(t, txn.HasQueuedJobs("non-existent-queue"))

	err = txn.Upsert([]*Job{job1.WithQueued(true)})
	require.NoError(t, err)
	assert.True(t, txn.HasQueuedJobs(job1.queue))
	assert.False(t, txn.HasQueuedJobs("non-existent-queue"))
}

func TestJobDb_TestQueuedJobs(t *testing.T) {
	jobDb := NewTestJobDb()
	jobs := make([]*Job, 10)
	for i := 0; i < len(jobs); i++ {
		jobs[i] = newJob().WithQueued(true)
		jobs[i].priority = 1000
		jobs[i].submittedTime = int64(i) // Ensures jobs are ordered.
	}
	shuffledJobs := slices.Clone(jobs)
	rand.Shuffle(len(shuffledJobs), func(i, j int) { shuffledJobs[i], shuffledJobs[j] = shuffledJobs[j], jobs[i] })
	txn := jobDb.WriteTxn()

	err := txn.Upsert(jobs)
	require.NoError(t, err)
	collect := func() []*Job {
		retrieved := make([]*Job, 0)
		iter := txn.QueuedJobs(jobs[0].Queue(), "pool", FairShareOrder)
		for !iter.Done() {
			j, _ := iter.Next()
			retrieved = append(retrieved, j)
		}
		return retrieved
	}

	assert.Equal(t, jobs, collect())

	// remove some jobs
	err = txn.BatchDelete([]string{jobs[1].id, jobs[3].id, jobs[5].id})
	require.NoError(t, err)
	assert.Equal(t, []*Job{jobs[0], jobs[2], jobs[4], jobs[6], jobs[7], jobs[8], jobs[9]}, collect())

	// dequeue some jobs
	err = txn.Upsert([]*Job{jobs[7].WithQueued(false), jobs[4].WithQueued(false)})
	require.NoError(t, err)
	assert.Equal(t, []*Job{jobs[0], jobs[2], jobs[6], jobs[8], jobs[9]}, collect())

	// change the priority of a job to put it to the front of the queue
	updatedJob := jobs[8].WithPriority(0)
	err = txn.Upsert([]*Job{updatedJob})
	require.NoError(t, err)
	assert.Equal(t, []*Job{updatedJob, jobs[0], jobs[2], jobs[6], jobs[9]}, collect())

	// new job
	job10 := newJob().WithPriority(90).WithQueued(true)
	err = txn.Upsert([]*Job{job10})
	require.NoError(t, err)
	assert.Equal(t, []*Job{updatedJob, job10, jobs[0], jobs[2], jobs[6], jobs[9]}, collect())

	// clear all jobs
	err = txn.BatchDelete([]string{updatedJob.id, job10.id, jobs[0].id, jobs[2].id, jobs[6].id, jobs[9].id})
	require.NoError(t, err)
	assert.Equal(t, []*Job{}, collect())
}

func TestJobDb_TestGetAll(t *testing.T) {
	jobDb := NewTestJobDb()
	job1 := newJob().WithNewRun("executor", "nodeId", "nodeName", "pool", 5)
	job2 := newJob().WithNewRun("executor", "nodeId", "nodeName", "pool", 10)
	txn := jobDb.WriteTxn()
	assert.Equal(t, []*Job{}, txn.GetAll())

	err := txn.Upsert([]*Job{job1, job2})
	require.NoError(t, err)
	actual := txn.GetAll()
	expected := []*Job{job1, job2}
	slices.SortFunc(expected, func(a, b *Job) int {
		if a.id > b.id {
			return -1
		} else if a.id < b.id {
			return 1
		} else {
			return 0
		}
	})
	slices.SortFunc(actual, func(a, b *Job) int {
		if a.id > b.id {
			return -1
		} else if a.id < b.id {
			return 1
		} else {
			return 0
		}
	})
	assert.Equal(t, expected, actual)
}

func TestJobDb_TestTransactions(t *testing.T) {
	jobDb := NewTestJobDb()
	job := newJob()

	txn1 := jobDb.WriteTxn()
	txn2 := jobDb.ReadTxn()
	err := txn1.Upsert([]*Job{job})
	require.NoError(t, err)

	assert.NotNil(t, txn1.GetById(job.id))
	assert.Nil(t, txn2.GetById(job.id))
	txn1.Commit()

	txn3 := jobDb.ReadTxn()
	assert.NotNil(t, txn3.GetById(job.id))

	assert.Error(t, txn1.Upsert([]*Job{job})) // should be error as you can't insert after committing
}

func TestJobDb_TestBatchDelete(t *testing.T) {
	jobDb := NewTestJobDb()
	job1 := newJob().WithQueued(true).WithNewRun("executor", "nodeId", "nodeName", "pool", 5)
	job2 := newJob().WithQueued(true).WithNewRun("executor", "nodeId", "nodeName", "pool", 10)
	txn := jobDb.WriteTxn()

	// Insert Job
	err := txn.Upsert([]*Job{job1, job2})
	require.NoError(t, err)
	err = txn.BatchDelete([]string{job2.Id()})
	require.NoError(t, err)
	assert.NotNil(t, txn.GetById(job1.Id()))
	assert.Nil(t, txn.GetById(job2.Id()))

	// Can't delete with read only transaction
	err = (jobDb.ReadTxn()).BatchDelete([]string{job1.Id()})
	require.Error(t, err)
}

func TestJobDb_GangInfoIsPopulated(t *testing.T) {
	tests := map[string]struct {
		annotations      map[string]string
		expectedGangInfo GangInfo
		expectError      bool
	}{
		"non-gang job": {
			expectedGangInfo: BasicJobGangInfo(),
			expectError:      false,
		},
		"non-gang job - with cardinality 1 gang annotations": {
			annotations: map[string]string{
				armadaconfiguration.GangCardinalityAnnotation:         "1",
				armadaconfiguration.GangIdAnnotation:                  "id",
				armadaconfiguration.GangNodeUniformityLabelAnnotation: "node-uniformity",
			},
			expectedGangInfo: BasicJobGangInfo(),
			expectError:      false,
		},
		"non-gang job - without gang id annotation": {
			annotations: map[string]string{
				armadaconfiguration.GangCardinalityAnnotation:         "2",
				armadaconfiguration.GangNodeUniformityLabelAnnotation: "node-uniformity",
			},
			expectedGangInfo: BasicJobGangInfo(),
			expectError:      false,
		},
		"gang job": {
			annotations: map[string]string{
				armadaconfiguration.GangCardinalityAnnotation:         "4",
				armadaconfiguration.GangIdAnnotation:                  "id",
				armadaconfiguration.GangNodeUniformityLabelAnnotation: "node-uniformity",
			},
			expectedGangInfo: CreateGangInfo("id", 4, "node-uniformity"),
			expectError:      false,
		},
		"invalid gang job - id empty": {
			annotations: map[string]string{
				armadaconfiguration.GangCardinalityAnnotation:         "4",
				armadaconfiguration.GangIdAnnotation:                  "",
				armadaconfiguration.GangNodeUniformityLabelAnnotation: "node-uniformity",
			},
			expectError: true,
		},
		"invalid gang job - cardinality is not an int": {
			annotations: map[string]string{
				armadaconfiguration.GangCardinalityAnnotation:         "error",
				armadaconfiguration.GangIdAnnotation:                  "",
				armadaconfiguration.GangNodeUniformityLabelAnnotation: "node-uniformity",
			},
			expectError: true,
		},
		"invalid gang job - cardinality is not a positive int": {
			annotations: map[string]string{
				armadaconfiguration.GangCardinalityAnnotation:         "0",
				armadaconfiguration.GangIdAnnotation:                  "",
				armadaconfiguration.GangNodeUniformityLabelAnnotation: "node-uniformity",
			},
			expectError: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			podRequirements := &internaltypes.PodRequirements{
				NodeSelector: map[string]string{"foo": "bar"},
				Annotations:  tc.annotations,
			}
			jobSchedulingInfo := &internaltypes.JobSchedulingInfo{
				PriorityClassName: "foo",
				PodRequirements:   podRequirements,
			}
			jobDb := NewTestJobDb()

			job, err := jobDb.NewJob("jobId", "jobSet", "queue", 1, jobSchedulingInfo, false, 0, false, false, false, 2, false, []string{}, 0)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, tc.expectedGangInfo, job.GetGangInfo())
			}
		})
	}
}

func TestJobDb_SchedulingKeyIsPopulated(t *testing.T) {
	podRequirements := &internaltypes.PodRequirements{
		NodeSelector: map[string]string{"foo": "bar"},
	}
	jobSchedulingInfo := &internaltypes.JobSchedulingInfo{
		PriorityClassName: "foo",
		PodRequirements:   podRequirements,
	}
	jobDb := NewTestJobDb()
	job, err := jobDb.NewJob("jobId", "jobSet", "queue", 1, jobSchedulingInfo, false, 0, false, false, false, 2, false, []string{}, 0)
	assert.Nil(t, err)
	assert.Equal(t, SchedulingKeyFromJob(jobDb.schedulingKeyGenerator, job), job.SchedulingKey())
}

func TestJobDb_SchedulingKey(t *testing.T) {
	tests := map[string]struct {
		podRequirementsA   *internaltypes.PodRequirements
		priorityClassNameA string
		podRequirementsB   *internaltypes.PodRequirements
		priorityClassNameB string
		equal              bool
	}{
		"annotations does not affect key": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				Annotations: map[string]string{
					"foo":  "bar",
					"fish": "chips",
					"salt": "pepper",
				},
				ResourceRequirements: v1.ResourceRequirements{
					Limits: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("1"),
						"memory":         resource.MustParse("2"),
						"nvidia.com/gpu": resource.MustParse("3"),
					},
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("2"),
						"memory":         resource.MustParse("2"),
						"nvidia.com/gpu": resource.MustParse("2"),
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				Annotations: map[string]string{
					"foo":  "bar",
					"fish": "chips",
				},
				ResourceRequirements: v1.ResourceRequirements{
					Limits: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("1"),
						"memory":         resource.MustParse("2"),
						"nvidia.com/gpu": resource.MustParse("3"),
					},
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("2"),
						"memory":         resource.MustParse("2"),
						"nvidia.com/gpu": resource.MustParse("2"),
					},
				},
			},
			equal: true,
		},
		"limits does not affect key": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Limits: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("1"),
						"memory":         resource.MustParse("2"),
						"nvidia.com/gpu": resource.MustParse("3"),
					},
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Limits: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("1"),
						"memory":         resource.MustParse("2"),
						"nvidia.com/gpu": resource.MustParse("4"),
					},
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			equal: true,
		},
		"priority": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			equal: true,
		},
		"zero request does not affect key": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property3": "value3",
					"property1": "value1",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
						"foo":            resource.MustParse("0"),
					},
				},
			},
			equal: true,
		},
		"nodeSelector key": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property3": "value3",
					"property1": "value1",
					"property2": "value2",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
		},
		"nodeSelector value": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property3": "value3",
					"property1": "value1-2",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
		},
		"nodeSelector different keys, same values": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"my-cool-label": "value",
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"my-other-cool-label": "value",
				},
			},
			equal: false,
		},
		"toleration key": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property3": "value3",
					"property1": "value1",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a-2",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
		},
		"toleration operator": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property3": "value3",
					"property1": "value1",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b-2",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
		},
		"toleration value": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property3": "value3",
					"property1": "value1",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b-2",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
		},
		"toleration effect": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property3": "value3",
					"property1": "value1",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d-2",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
		},
		"toleration tolerationSeconds": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property3": "value3",
					"property1": "value1",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(2),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			equal: true,
		},
		"key ordering": {
			podRequirementsA: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property1": "value1",
					"property3": "value3",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"cpu":            resource.MustParse("4"),
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				NodeSelector: map[string]string{
					"property3": "value3",
					"property1": "value1",
				},
				Tolerations: []v1.Toleration{{
					Key:               "a",
					Operator:          "b",
					Value:             "b",
					Effect:            "d",
					TolerationSeconds: pointer.Int64(1),
				}},
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						"memory":         resource.MustParse("5"),
						"nvidia.com/gpu": resource.MustParse("6"),
						"cpu":            resource.MustParse("4"),
					},
				},
			},
			equal: true,
		},
		"affinity PodAffinity ignored": {
			podRequirementsA: &internaltypes.PodRequirements{
				Affinity: &v1.Affinity{
					NodeAffinity: &v1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
							NodeSelectorTerms: []v1.NodeSelectorTerm{
								{
									MatchExpressions: []v1.NodeSelectorRequirement{
										{
											Key:      "k1",
											Operator: "o1",
											Values:   []string{"v1", "v2"},
										},
									},
									MatchFields: []v1.NodeSelectorRequirement{
										{
											Key:      "k2",
											Operator: "o2",
											Values:   []string{"v10", "v20"},
										},
									},
								},
							},
						},
					},
					PodAffinity: &v1.PodAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
							{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"label1": "labelval1",
										"label2": "labelval2",
										"label3": "labelval3",
									},
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{
											Key:      "k1",
											Operator: "o1",
											Values:   []string{"v1", "v2", "v3"},
										},
									},
								},
								Namespaces:  []string{"n1, n2, n3"},
								TopologyKey: "topkey1",
								NamespaceSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"label10": "labelval1",
										"label20": "labelval2",
										"label30": "labelval3",
									},
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{
											Key:      "k10",
											Operator: "o10",
											Values:   []string{"v10", "v20", "v30"},
										},
									},
								},
							},
						},
					},
					PodAntiAffinity: nil,
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				Affinity: &v1.Affinity{
					NodeAffinity: &v1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
							NodeSelectorTerms: []v1.NodeSelectorTerm{
								{
									MatchExpressions: []v1.NodeSelectorRequirement{
										{
											Key:      "k1",
											Operator: "o1",
											Values:   []string{"v1", "v2"},
										},
									},
									MatchFields: []v1.NodeSelectorRequirement{
										{
											Key:      "k2",
											Operator: "o2",
											Values:   []string{"v10", "v20"},
										},
									},
								},
							},
						},
					},
					PodAffinity: &v1.PodAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []v1.PodAffinityTerm{
							{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"label1": "labelval1-2",
										"label2": "labelval2",
										"label3": "labelval3",
									},
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{
											Key:      "k1",
											Operator: "o1",
											Values:   []string{"v1", "v2", "v3"},
										},
									},
								},
								Namespaces:  []string{"n1, n2, n3"},
								TopologyKey: "topkey1",
								NamespaceSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"label10": "labelval1",
										"label20": "labelval2",
										"label30": "labelval3",
									},
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{
											Key:      "k10",
											Operator: "o10",
											Values:   []string{"v10", "v20", "v30"},
										},
									},
								},
							},
						},
					},
					PodAntiAffinity: nil,
				},
			},
			equal: true,
		},
		"affinity NodeAffinity MatchExpressions": {
			podRequirementsA: &internaltypes.PodRequirements{
				Affinity: &v1.Affinity{
					NodeAffinity: &v1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
							NodeSelectorTerms: []v1.NodeSelectorTerm{
								{
									MatchExpressions: []v1.NodeSelectorRequirement{
										{
											Key:      "k1",
											Operator: "o1",
											Values:   []string{"v1", "v2"},
										},
									},
									MatchFields: []v1.NodeSelectorRequirement{
										{
											Key:      "k2",
											Operator: "o2",
											Values:   []string{"v10", "v20"},
										},
									},
								},
							},
						},
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				Affinity: &v1.Affinity{
					NodeAffinity: &v1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
							NodeSelectorTerms: []v1.NodeSelectorTerm{
								{
									MatchExpressions: []v1.NodeSelectorRequirement{
										{
											Key:      "k1",
											Operator: "o1",
											Values:   []string{"v1", "v3"},
										},
									},
									MatchFields: []v1.NodeSelectorRequirement{
										{
											Key:      "k2",
											Operator: "o2",
											Values:   []string{"v10", "v20"},
										},
									},
								},
							},
						},
					},
				},
			},
			equal: false,
		},
		"affinity NodeAffinity MatchFields": {
			podRequirementsA: &internaltypes.PodRequirements{
				Affinity: &v1.Affinity{
					NodeAffinity: &v1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
							NodeSelectorTerms: []v1.NodeSelectorTerm{
								{
									MatchExpressions: []v1.NodeSelectorRequirement{
										{
											Key:      "k1",
											Operator: "o1",
											Values:   []string{"v1", "v2"},
										},
									},
									MatchFields: []v1.NodeSelectorRequirement{
										{
											Key:      "k2",
											Operator: "o2",
											Values:   []string{"v10", "v20"},
										},
									},
								},
							},
						},
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				Affinity: &v1.Affinity{
					NodeAffinity: &v1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
							NodeSelectorTerms: []v1.NodeSelectorTerm{
								{
									MatchExpressions: []v1.NodeSelectorRequirement{
										{
											Key:      "k1",
											Operator: "o1",
											Values:   []string{"v1", "v2"},
										},
									},
									MatchFields: []v1.NodeSelectorRequirement{
										{
											Key:      "k2",
											Operator: "o2",
											Values:   []string{"v10", "v21"},
										},
									},
								},
							},
						},
					},
				},
			},
			equal: false,
		},
		"affinity NodeAffinity multiple MatchFields": {
			podRequirementsA: &internaltypes.PodRequirements{
				Affinity: &v1.Affinity{
					NodeAffinity: &v1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
							NodeSelectorTerms: []v1.NodeSelectorTerm{
								{
									MatchExpressions: []v1.NodeSelectorRequirement{
										{
											Key:      "k1",
											Operator: "o1",
											Values:   []string{"v1", "v2"},
										},
									},
									MatchFields: []v1.NodeSelectorRequirement{
										{
											Key:      "k2",
											Operator: "o2",
											Values:   []string{"v10", "v20"},
										},
									},
								},
							},
						},
					},
				},
			},
			podRequirementsB: &internaltypes.PodRequirements{
				Affinity: &v1.Affinity{
					NodeAffinity: &v1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
							NodeSelectorTerms: []v1.NodeSelectorTerm{
								{
									MatchExpressions: []v1.NodeSelectorRequirement{
										{
											Key:      "k1",
											Operator: "o1",
											Values:   []string{"v1", "v2"},
										},
									},
									MatchFields: []v1.NodeSelectorRequirement{
										{
											Key:      "k2",
											Operator: "o2",
											Values:   []string{"v10", "v20"},
										},
										{
											Key:      "k3",
											Operator: "o2",
											Values:   []string{"v10", "v20"},
										},
									},
								},
							},
						},
					},
				},
			},
			equal: false,
		},
		"priority class names equal": {
			podRequirementsA: &internaltypes.PodRequirements{
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{"cpu": resource.MustParse("2")},
				},
			},
			priorityClassNameA: "my-cool-priority-class",
			podRequirementsB: &internaltypes.PodRequirements{
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{"cpu": resource.MustParse("2")},
				},
			},
			priorityClassNameB: "my-cool-priority-class",
			equal:              true,
		},
		"priority class names different": {
			podRequirementsA: &internaltypes.PodRequirements{
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{"cpu": resource.MustParse("2")},
				},
			},
			priorityClassNameA: "my-cool-priority-class",
			podRequirementsB: &internaltypes.PodRequirements{
				ResourceRequirements: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{"cpu": resource.MustParse("2")},
				},
			},
			priorityClassNameB: "my-cool-other-priority-class",
			equal:              false,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			skg := internaltypes.NewSchedulingKeyGenerator()

			jobSchedulingInfoA := jobSchedulingInfo.DeepCopy()
			jobSchedulingInfoA.PriorityClassName = tc.priorityClassNameA
			jobSchedulingInfoA.PodRequirements = tc.podRequirementsA
			jobA := JobWithJobSchedulingInfo(baseJob, jobSchedulingInfoA)

			jobSchedulingInfoB := jobSchedulingInfo.DeepCopy()
			jobSchedulingInfoB.PriorityClassName = tc.priorityClassNameB
			jobSchedulingInfoB.PodRequirements = tc.podRequirementsB
			jobB := JobWithJobSchedulingInfo(baseJob, jobSchedulingInfoB)

			schedulingKeyA := SchedulingKeyFromJob(skg, jobA)
			schedulingKeyB := SchedulingKeyFromJob(skg, jobB)

			// Generate the keys several times to check their consistency.
			for i := 1; i < 10; i++ {
				assert.Equal(t, SchedulingKeyFromJob(skg, jobA), schedulingKeyA)
				assert.Equal(t, SchedulingKeyFromJob(skg, jobB), schedulingKeyB)
			}

			if tc.equal {
				assert.Equal(t, schedulingKeyA, schedulingKeyB)
			} else {
				assert.NotEqual(t, schedulingKeyA, schedulingKeyB)
			}
		})
	}
}

func newJob() *Job {
	return &Job{
		jobDb:             NewTestJobDb(),
		id:                util.NewULID(),
		queue:             "test-queue",
		priority:          0,
		submittedTime:     0,
		queued:            false,
		runsById:          map[string]*JobRun{},
		jobSchedulingInfo: jobSchedulingInfo,
		pools:             []string{"pool"},
	}
}

func newGangJob() *Job {
	gangInfo, err := createGangInfo(gangJobSchedulingInfo)
	if err != nil {
		panic(err)
	}
	return &Job{
		jobDb:             NewTestJobDb(),
		id:                util.NewULID(),
		queue:             "test-queue",
		priority:          0,
		submittedTime:     0,
		queued:            false,
		runsById:          map[string]*JobRun{},
		jobSchedulingInfo: gangJobSchedulingInfo,
		pools:             []string{"pool"},
		gangInfo:          *gangInfo,
	}
}
