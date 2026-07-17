package state

import (
	"context"
	"time"

	"github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
)

func (s *LocalStore) SaveReleaseCandidate(ctx context.Context, deployment contract.Deployment, syncedAt time.Time) (catalog.ReleaseCandidate, error) {
	prepared, err := catalog.PrepareReleaseCandidate(deployment, syncedAt)
	if err != nil {
		return catalog.ReleaseCandidate{}, err
	}
	result := prepared
	err = s.update(ctx, func(snapshot *Snapshot, _ time.Time) error {
		releaseCatalog := &snapshot.ReleaseCatalog
		catalog.NormalizeSnapshot(releaseCatalog)
		key := catalog.ReleaseCandidateKey(
			prepared.Deployment.SourceWorkspace(),
			prepared.Deployment.SourceGitSourceID(),
			prepared.Deployment.Commit,
		)
		if existing, ok := releaseCatalog.Candidates[key]; ok {
			result = existing
			return nil
		}
		releaseCatalog.Candidates[key] = prepared
		return nil
	})
	return result, err
}

func (s *LocalStore) GetReleaseCandidate(ctx context.Context, workspace string, gitSourceID string, commit string) (catalog.ReleaseCandidate, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return catalog.ReleaseCandidate{}, err
	}
	catalog.NormalizeSnapshot(&snapshot.ReleaseCatalog)
	candidate, ok := snapshot.ReleaseCatalog.Candidates[catalog.ReleaseCandidateKey(workspace, gitSourceID, commit)]
	if !ok {
		return catalog.ReleaseCandidate{}, catalog.ErrReleaseCandidateNotFound
	}
	return candidate, nil
}

func (s *LocalStore) GetLatestReleaseCandidate(ctx context.Context, workspace string, gitSourceID string) (catalog.ReleaseCandidate, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return catalog.ReleaseCandidate{}, err
	}
	catalog.NormalizeSnapshot(&snapshot.ReleaseCatalog)
	workspace = contract.NormalizeWorkspace(workspace)
	gitSourceID = contract.NormalizeGitSourceID(gitSourceID, "")
	var latest catalog.ReleaseCandidate
	found := false
	for _, candidate := range snapshot.ReleaseCatalog.Candidates {
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
		return catalog.ReleaseCandidate{}, catalog.ErrReleaseCandidateNotFound
	}
	return latest, nil
}

var _ catalog.ReleaseCandidateStore = (*LocalStore)(nil)
