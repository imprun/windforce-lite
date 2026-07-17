package state

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/imprun/windforce-core/internal/contract"
)

type jobRunRecord struct {
	Job Job
	Run Run
}

func listJobsFromRecords(records []jobRunRecord, query JobListQuery) []JobListItem {
	query.WorkspaceID = contract.NormalizeWorkspace(query.WorkspaceID)
	if query.Status == "" {
		query.Status = "all"
	}
	items := make([]JobListItem, 0, len(records))
	for _, record := range records {
		job := record.Job
		run := record.Run
		if normalizedJobWorkspace("", job) != query.WorkspaceID {
			continue
		}
		if query.AppKey != "" && job.Payload.App != query.AppKey {
			continue
		}
		if query.ActionKey != "" && job.Payload.Action != query.ActionKey {
			continue
		}
		if query.TriggerKind != "" && jobTriggerKind(job) != query.TriggerKind {
			continue
		}
		if query.Since != nil && job.CreatedAt.Before(*query.Since) {
			continue
		}
		if query.Until != nil && job.CreatedAt.After(*query.Until) {
			continue
		}
		item := newJobListItem(query.WorkspaceID, job, run)
		if !jobStatusMatches(query.Status, item.Status) {
			continue
		}
		if query.CursorCreatedAt != nil {
			if job.CreatedAt.After(*query.CursorCreatedAt) || job.CreatedAt.Equal(*query.CursorCreatedAt) && job.ID >= query.CursorID {
				continue
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i int, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].ID > items[j].ID
	})
	if query.Limit > 0 && len(items) > query.Limit {
		items = items[:query.Limit]
	}
	return items
}

func summarizeJobs(records []jobRunRecord, workspaceID string, recent time.Duration) JobSummary {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	if recent <= 0 {
		recent = 24 * time.Hour
	}
	cutoff := time.Now().UTC().Add(-recent)
	summary := JobSummary{
		ByTag: []JobSummaryTagBreakdown{},
		ByApp: []JobSummaryAppBreakdown{},
	}
	byTag := map[string]*JobSummaryCounts{}
	byApp := map[string]*JobSummaryCounts{}
	for _, record := range records {
		job := record.Job
		run := record.Run
		if normalizedJobWorkspace("", job) != workspaceID {
			continue
		}
		status := jobTerminalStatus(job, run)
		tag := jobTag(job)
		app := job.Payload.App
		tagCounts := byTag[tag]
		if tagCounts == nil {
			tagCounts = &JobSummaryCounts{}
			byTag[tag] = tagCounts
		}
		appCounts := byApp[app]
		if appCounts == nil {
			appCounts = &JobSummaryCounts{}
			byApp[app] = appCounts
		}
		countJobSummary(&summary.JobSummaryCounts, job, run, status, cutoff)
		countJobSummary(tagCounts, job, run, status, cutoff)
		countJobSummary(appCounts, job, run, status, cutoff)
		if job.State == JobQueued && (summary.OldestQueuedAt == nil || job.CreatedAt.Before(*summary.OldestQueuedAt)) {
			value := job.CreatedAt
			summary.OldestQueuedAt = &value
		}
	}
	tags := make([]string, 0, len(byTag))
	for tag := range byTag {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		counts := *byTag[tag]
		if includeJobSummaryBreakdown(counts) {
			summary.ByTag = append(summary.ByTag, JobSummaryTagBreakdown{Tag: tag, JobSummaryCounts: counts})
		}
	}
	apps := make([]string, 0, len(byApp))
	for app := range byApp {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	for _, app := range apps {
		counts := *byApp[app]
		if includeJobSummaryBreakdown(counts) {
			summary.ByApp = append(summary.ByApp, JobSummaryAppBreakdown{AppKey: app, JobSummaryCounts: counts})
		}
	}
	return summary
}

func includeJobSummaryBreakdown(counts JobSummaryCounts) bool {
	return counts.QueuedCount > 0 || counts.RunningCount > 0 || counts.CompletedCountRecent > 0
}

func countJobSummary(counts *JobSummaryCounts, job Job, run Run, status string, cutoff time.Time) {
	switch job.State {
	case JobQueued:
		counts.QueuedCount++
	case JobRunning:
		counts.RunningCount++
	}
	if job.State == JobSucceeded || job.State == JobFailed || IsTerminal(run) {
		completedAt := run.UpdatedAt
		if completedAt.IsZero() {
			completedAt = job.UpdatedAt
		}
		if !completedAt.Before(cutoff) {
			counts.CompletedCountRecent++
			switch status {
			case "failure":
				counts.FailedCountRecent++
			case "canceled":
				counts.CanceledCountRecent++
			}
		}
	}
}

func newJobListItem(workspaceID string, job Job, run Run) JobListItem {
	status := jobTerminalStatus(job, run)
	var startedAt *time.Time
	var completedAt *time.Time
	var worker *string
	startedAt = job.StartedAt
	if job.LeaseOwner != "" {
		worker = stringPtr(job.LeaseOwner)
	}
	if job.State == JobRunning {
		if startedAt == nil {
			startedAt = &job.UpdatedAt
		}
	}
	if job.State == JobSucceeded || job.State == JobFailed || IsTerminal(run) {
		completedAt = &run.UpdatedAt
	}
	var durationMs int64
	if run.Result != nil {
		durationMs = run.Result.DurationMs
	}
	return JobListItem{
		ID:             job.ID,
		WorkspaceID:    contract.NormalizeWorkspace(workspaceID),
		AppKey:         job.Payload.App,
		ActionKey:      job.Payload.Action,
		TriggerKind:    jobTriggerKind(job),
		Status:         status,
		Queued:         job.State == JobQueued,
		Running:        job.State == JobRunning,
		Completed:      job.State == JobSucceeded || job.State == JobFailed || IsTerminal(run),
		CreatedAt:      job.CreatedAt,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
		DurationMs:     durationMs,
		Worker:         worker,
		GitSourceID:    numericStringPtr(job.Payload.GitSourceID),
		CommitSha:      stringPtr(job.Payload.Commit),
		Entrypoint:     jobEntrypoint(job),
		Tag:            jobTag(job),
		CreatedBy:      firstNonEmpty(strings.TrimSpace(job.Payload.CreatedBy), strings.TrimSpace(run.CreatedBy), defaultActorSubject),
		PermissionedAs: firstNonEmpty(strings.TrimSpace(job.Payload.PermissionedAs), strings.TrimSpace(run.PermissionedAs), strings.TrimSpace(job.Payload.CreatedBy), strings.TrimSpace(run.CreatedBy), defaultActorSubject),
		CanceledBy:     firstPresentStringPtr(job.CanceledBy, canceledBy(run)),
		CanceledReason: firstPresentStringPtr(job.CanceledReason, canceledReason(run)),
		FlowRunID:      stringPtr(job.Payload.FlowRunID),
		FlowStepID:     stringPtr(job.Payload.FlowStepID),
		ErrorSnippet:   failureSnippet(status, run),
	}
}

func jobEntrypoint(job Job) string {
	if entrypoint := strings.TrimSpace(job.Payload.PinnedDeployment().Entrypoint); entrypoint != "" {
		return entrypoint
	}
	return strings.TrimSpace(job.Payload.ActionSpec.Entrypoint)
}

func jobStatusMatches(filter string, status string) bool {
	switch filter {
	case "", "all":
		return true
	case "completed":
		return status == "success" || status == "failure" || status == "canceled"
	case "success":
		return status == "success"
	case "failure":
		return status == "failure"
	default:
		return status == filter
	}
}

func jobTerminalStatus(job Job, run Run) string {
	if run.State == RunCanceled {
		return "canceled"
	}
	if job.State == JobQueued {
		return "queued"
	}
	if job.State == JobRunning {
		return "running"
	}
	if job.State == JobSucceeded || run.State == RunSucceeded || run.State == RunWaitingHuman {
		return "success"
	}
	return "failure"
}

func jobTriggerKind(job Job) string {
	if job.Payload.TriggerKind != "" {
		return job.Payload.TriggerKind
	}
	if job.Payload.CorrelationID != "" {
		return "api"
	}
	return "api"
}

func jobTag(job Job) string {
	if strings.TrimSpace(job.Payload.Tag) != "" {
		return strings.TrimSpace(job.Payload.Tag)
	}
	return contract.EffectiveRouteTagForAction(job.Payload.PinnedDeployment(), job.Payload.ActionSpec)
}

func jobAppKey(job Job) string {
	if app := strings.TrimSpace(job.Payload.App); app != "" {
		return app
	}
	return strings.TrimSpace(job.Payload.PinnedDeployment().App)
}

func jobMaxConcurrent(job Job) (int, bool) {
	deployment := job.Payload.PinnedDeployment()
	if deployment.MaxConcurrent == nil || *deployment.MaxConcurrent <= 0 {
		return 0, false
	}
	return int(*deployment.MaxConcurrent), true
}

func maxConcurrentReached(snapshot *Snapshot, candidate Job) bool {
	limit, ok := jobMaxConcurrent(candidate)
	if !ok {
		return false
	}
	workspaceID := normalizedJobWorkspace("", candidate)
	appKey := jobAppKey(candidate)
	if appKey == "" {
		return false
	}
	running := 0
	for _, job := range snapshot.Jobs {
		if job.State != JobRunning {
			continue
		}
		if normalizedJobWorkspace("", job) == workspaceID && jobAppKey(job) == appKey {
			running++
		}
	}
	return running >= limit
}

func normalizeClaimTags(tags []string) map[string]struct{} {
	normalized := map[string]struct{}{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		normalized[tag] = struct{}{}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// jobRequiredLabels resolves a job's pinned label requirements. Legacy jobs
// that predate labels carry only requiredCapabilities, which are labels by
// their old manifest name (ADR 0009).
func jobRequiredLabels(job Job) []string {
	if job.Payload.RequiredLabels != nil {
		return job.Payload.RequiredLabels
	}
	return job.Payload.RequiredCapabilities
}

// claimAllowed enforces both claim dimensions (ADR 0009):
//   - explicit route tags keep their legacy semantics — a worker with no
//     tags serves every tag, otherwise the job's tag must be in the set;
//   - labels use subset containment — the worker's label set must cover the
//     job's required labels, so a worker with no labels claims only
//     unconstrained jobs.
func claimAllowed(job Job, tags map[string]struct{}, labels map[string]struct{}) bool {
	if len(tags) > 0 {
		if _, ok := tags[jobTag(job)]; !ok {
			return false
		}
	}
	for _, required := range jobRequiredLabels(job) {
		if _, ok := labels[required]; !ok {
			return false
		}
	}
	return true
}

func canceledReason(run Run) *string {
	if run.State != RunCanceled || len(run.Error) == 0 {
		return nil
	}
	var payload struct {
		Message        string  `json:"message"`
		CanceledReason *string `json:"canceledReason"`
	}
	if json.Unmarshal(run.Error, &payload) == nil {
		if payload.CanceledReason != nil {
			return payload.CanceledReason
		}
		if payload.Message != "" {
			return stringPtr(payload.Message)
		}
	}
	return nil
}

func canceledReasonValue(job Job) string {
	if job.CanceledReason == nil {
		return ""
	}
	return *job.CanceledReason
}

func canceledBy(run Run) *string {
	if run.State != RunCanceled || len(run.Error) == 0 {
		return nil
	}
	var payload struct {
		CanceledBy string `json:"canceledBy"`
	}
	if json.Unmarshal(run.Error, &payload) == nil {
		return stringPtr(strings.TrimSpace(payload.CanceledBy))
	}
	return nil
}

func firstPresentStringPtr(values ...*string) *string {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

const errorSnippetMax = 200

func failureSnippet(status string, run Run) *string {
	if status != "failure" {
		return nil
	}
	if run.Result != nil {
		if len(run.Result.Output) > 0 {
			if snippet := errorSnippet(run.Result.Output); snippet != nil {
				return snippet
			}
		}
		if run.Result.Error != "" {
			return errorSnippet([]byte(run.Result.Error))
		}
	}
	if len(run.Error) > 0 {
		return errorSnippet(run.Error)
	}
	return nil
}

func errorSnippet(plain []byte) *string {
	var workerError struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	text := ""
	if err := json.Unmarshal(plain, &workerError); err == nil && workerError.Message != "" {
		if workerError.Name != "" {
			text = workerError.Name + ": " + workerError.Message
		} else {
			text = workerError.Message
		}
	} else {
		text = string(plain)
	}
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return nil
	}
	runes := []rune(text)
	if len(runes) > errorSnippetMax {
		text = string(runes[:errorSnippetMax]) + "\u2026"
	}
	return stringPtr(text)
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func numericStringPtr(value string) *int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return nil
	}
	return &parsed
}

func eventPayload(correlationID string, values map[string]any) map[string]any {
	if values == nil {
		values = map[string]any{}
	}
	if correlationID != "" {
		values["correlationId"] = correlationID
	}
	return values
}

func mergeResumeInput(original json.RawMessage, taskID string, resumeInput json.RawMessage) json.RawMessage {
	if len(original) == 0 || !json.Valid(original) {
		original = json.RawMessage("{}")
	}
	if len(resumeInput) == 0 || !json.Valid(resumeInput) {
		resumeInput = json.RawMessage("{}")
	}
	resume := mustRaw(map[string]any{
		"humanTaskID": taskID,
		"input":       json.RawMessage(resumeInput),
	})

	var object map[string]json.RawMessage
	if json.Unmarshal(original, &object) == nil && object != nil {
		object["$resume"] = resume
		data, _ := json.Marshal(object)
		return data
	}
	data, _ := json.Marshal(map[string]json.RawMessage{
		"$input":  original,
		"$resume": resume,
	})
	return data
}

func mustRaw(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{"error":"event payload marshal failed"}`)
	}
	return data
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func cloneResult(result contract.JobResult) *contract.JobResult {
	cloned := result
	cloned.Output = cloneRaw(result.Output)
	return &cloned
}

func normalizedJobWorkspace(workspaceID string, job Job) string {
	if workspaceID == "" {
		workspaceID = job.Payload.Workspace
	}
	if workspaceID == "" {
		workspaceID = job.Payload.PinnedDeployment().SourceWorkspace()
	}
	return contract.NormalizeWorkspace(workspaceID)
}
