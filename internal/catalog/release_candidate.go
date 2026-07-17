package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
)

var ErrReleaseCandidateNotFound = errors.New("release candidate not found")

// ReleaseCandidate is an immutable, materialized source revision that can be
// published without reading Git again.
type ReleaseCandidate struct {
	Deployment contract.Deployment `json:"deployment"`
	SyncedAt   time.Time           `json:"syncedAt"`
}

type ReleaseCandidateStore interface {
	SaveReleaseCandidate(ctx context.Context, deployment contract.Deployment, syncedAt time.Time) (ReleaseCandidate, error)
	GetReleaseCandidate(ctx context.Context, workspace string, gitSourceID string, commit string) (ReleaseCandidate, error)
	GetLatestReleaseCandidate(ctx context.Context, workspace string, gitSourceID string) (ReleaseCandidate, error)
}

type SourceOperationLeaseStore interface {
	AcquireSourceOperationLease(ctx context.Context, workspace string, gitSourceID string, holder string, ttl time.Duration) (bool, error)
	RenewSourceOperationLease(ctx context.Context, workspace string, gitSourceID string, holder string, ttl time.Duration) (bool, error)
	ReleaseSourceOperationLease(ctx context.Context, workspace string, gitSourceID string, holder string) error
}

func ReleaseCandidateKey(workspace string, gitSourceID string, commit string) string {
	return contract.NormalizeWorkspace(workspace) + "/" +
		contract.NormalizeGitSourceID(gitSourceID, "") + "/" + strings.TrimSpace(commit)
}

func PrepareReleaseCandidate(deployment contract.Deployment, syncedAt time.Time) (ReleaseCandidate, error) {
	if syncedAt.IsZero() {
		syncedAt = time.Now().UTC()
	}
	deployment = NormalizeDeploymentDefaults(deployment)
	workspace := deployment.SourceWorkspace()
	gitSourceID := deployment.SourceGitSourceID()
	if strings.TrimSpace(gitSourceID) == "" {
		return ReleaseCandidate{}, errors.New("candidate git source id is required")
	}
	if strings.TrimSpace(deployment.Commit) == "" {
		return ReleaseCandidate{}, errors.New("candidate commit is required")
	}
	if strings.TrimSpace(deployment.App) == "" {
		return ReleaseCandidate{}, errors.New("candidate app is required")
	}
	deployment.Workspace = workspace
	deployment.GitSourceID = gitSourceID
	return ReleaseCandidate{Deployment: deployment, SyncedAt: syncedAt.UTC()}, nil
}

func ValidateReleaseCandidate(candidate ReleaseCandidate, workspace string, gitSourceID string, commit string) error {
	expected := ReleaseCandidateKey(workspace, gitSourceID, commit)
	actual := ReleaseCandidateKey(
		candidate.Deployment.SourceWorkspace(),
		candidate.Deployment.SourceGitSourceID(),
		candidate.Deployment.Commit,
	)
	if expected != actual {
		return fmt.Errorf("release candidate identity mismatch: got %s, want %s", actual, expected)
	}
	return nil
}

func (c *FileCatalog) SaveReleaseCandidate(ctx context.Context, deployment contract.Deployment, syncedAt time.Time) (ReleaseCandidate, error) {
	if err := ctx.Err(); err != nil {
		return ReleaseCandidate{}, err
	}
	candidate, err := PrepareReleaseCandidate(deployment, syncedAt)
	if err != nil {
		return ReleaseCandidate{}, err
	}
	snapshot, err := c.Load(ctx)
	if err != nil {
		return ReleaseCandidate{}, err
	}
	key := ReleaseCandidateKey(candidate.Deployment.SourceWorkspace(), candidate.Deployment.SourceGitSourceID(), candidate.Deployment.Commit)
	if existing, ok := snapshot.Candidates[key]; ok {
		return existing, nil
	}
	snapshot.Candidates[key] = candidate
	if err := c.write(snapshot); err != nil {
		return ReleaseCandidate{}, err
	}
	return candidate, nil
}

func (c *FileCatalog) GetReleaseCandidate(ctx context.Context, workspace string, gitSourceID string, commit string) (ReleaseCandidate, error) {
	snapshot, err := c.Load(ctx)
	if err != nil {
		return ReleaseCandidate{}, err
	}
	candidate, ok := snapshot.Candidates[ReleaseCandidateKey(workspace, gitSourceID, commit)]
	if !ok {
		return ReleaseCandidate{}, ErrReleaseCandidateNotFound
	}
	return candidate, nil
}

func (c *FileCatalog) GetLatestReleaseCandidate(ctx context.Context, workspace string, gitSourceID string) (ReleaseCandidate, error) {
	snapshot, err := c.Load(ctx)
	if err != nil {
		return ReleaseCandidate{}, err
	}
	workspace = contract.NormalizeWorkspace(workspace)
	gitSourceID = contract.NormalizeGitSourceID(gitSourceID, "")
	var latest ReleaseCandidate
	found := false
	for _, candidate := range snapshot.Candidates {
		if candidate.Deployment.SourceWorkspace() != workspace || candidate.Deployment.SourceGitSourceID() != gitSourceID {
			continue
		}
		if !found || candidate.SyncedAt.After(latest.SyncedAt) ||
			(candidate.SyncedAt.Equal(latest.SyncedAt) && candidate.Deployment.Commit > latest.Deployment.Commit) {
			latest = candidate
			found = true
		}
	}
	if !found {
		return ReleaseCandidate{}, ErrReleaseCandidateNotFound
	}
	return latest, nil
}

var _ ReleaseCandidateStore = (*FileCatalog)(nil)
