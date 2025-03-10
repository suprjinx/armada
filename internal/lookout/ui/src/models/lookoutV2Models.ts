// Values must match the server-side states
export enum JobState {
  Queued = "QUEUED",
  Pending = "PENDING",
  Running = "RUNNING",
  Succeeded = "SUCCEEDED",
  Failed = "FAILED",
  Cancelled = "CANCELLED",
}

export const jobStateDisplayInfo: Record<JobState, ColoredState> = {
  [JobState.Queued]: { displayName: "Queued", color: "#ffff00" },
  [JobState.Pending]: { displayName: "Pending", color: "#ff9900" },
  [JobState.Running]: { displayName: "Running", color: "#00ff00" },
  [JobState.Succeeded]: { displayName: "Succeeded", color: "#0000ff" },
  [JobState.Failed]: { displayName: "Failed", color: "#ff0000" },
  [JobState.Cancelled]: { displayName: "Cancelled", color: "#999999" },
}

const terminatedJobStates = new Set([JobState.Succeeded, JobState.Failed, JobState.Cancelled])
export const isTerminatedJobState = (state: JobState) => terminatedJobStates.has(state)

export const formatJobState = (state: JobState): string => jobStateDisplayInfo[state]?.displayName || state

export const JobRunStates: Record<string, ColoredState> = {
  RunPending: { displayName: "Run Pending", color: "#ff9900" },
  RunRunning: { displayName: "Run Running", color: "#00ff00" },
  RunSucceeded: { displayName: "Run Succeeded", color: "#0000ff" },
  RunFailed: { displayName: "Run Failed", color: "#ff0000" },
  RunTerminated: { displayName: "Terminated", color: "#ffffff" },
  RunPreempted: { displayName: "Preempted", color: "#ffff00" },
  RunUnableToSchedule: { displayName: "Unable to Schedule", color: "#ff0000" },
  RunLeaseReturned: { displayName: "Lease Returned", color: "#ff0000" },
  RunLeaseExpired: { displayName: "Lease Expired", color: "#ff0000" },
  RunMaxRunsExceeded: { displayName: "Max Runs Exceeded", color: "#ff0000" },
}

type ColoredState = {
  displayName: string
  color: string
}

export type JobId = string

export type Job = {
  jobId: JobId
  queue: string
  owner: string
  jobSet: string
  state: JobState
  cpu: number
  memory: number
  ephemeralStorage: number
  gpu: number
  priority: number
  priorityClass: string
  submitted: string
  cancelled?: string
  timeInState: string
  duplicate: boolean
  annotations: Record<string, string>
  runs: JobRun[]
  lastActiveRunId?: string
}

export type JobKey = keyof Job

export type JobRun = {
  runId: string
  jobId: string
  cluster: string
  node?: string
  pending: string
  started?: string
  finished?: string
  jobRunState: string
  error?: string
  exitCode?: number
}

export enum Match {
  Exact = "exact",
  StartsWith = "startsWith",
  GreaterThan = "greater",
  LessThan = "less",
  GreaterThanOrEqual = "greaterOrEqual",
  LessThanOrEqual = "lessOrEqual",
  AnyOf = "anyOf",
}

export type JobFilter = {
  field: string
  value: string | number | string[] | number[]
  match: Match
}

export type JobGroup = {
  name: string
  count: number
  aggregates: Record<string, string | number>
}

export type SortDirection = "ASC" | "DESC"

export type JobOrder = {
  field: string
  direction: SortDirection
}
