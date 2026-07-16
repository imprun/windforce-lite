package state

import (
	"context"
	"time"

	"github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
)

var _ catalog.Store = (*LocalStore)(nil)

func (s *LocalStore) PublishRelease(ctx context.Context, deployment contract.Deployment, releasedAt time.Time) (contract.Deployment, error) {
	var published contract.Deployment
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		if releasedAt.IsZero() {
			releasedAt = now
		}
		var history catalog.DeploymentHistory
		var audit catalog.AuditRecord
		published, history, audit = catalog.PreparePublication(deployment, releasedAt)
		releaseCatalog := &snapshot.ReleaseCatalog
		catalog.NormalizeSnapshot(releaseCatalog)
		previous := latestReleaseHistory(*releaseCatalog, published.SourceWorkspace(), published.App)
		releaseCatalog.Deployments[catalog.DeploymentKey(published.SourceWorkspace(), published.App)] = published
		releaseCatalog.History = append(releaseCatalog.History, history)
		releaseCatalog.Audit = append(releaseCatalog.Audit, audit)
		marker := catalog.SourceReleaseMarker{
			Workspace:   published.SourceWorkspace(),
			GitSourceID: published.SourceGitSourceID(),
			Commit:      published.Commit,
			ReleasedAt:  history.CreatedAt,
		}
		releaseCatalog.SourceMarkers[catalog.SourceReleaseKey(marker.Workspace, marker.GitSourceID)] = marker
		releaseEvent, err := prepareReleaseEvent(history, previous)
		if err != nil {
			return err
		}
		snapshot.ControlPlaneEvents[releaseEvent.ID] = releaseEvent
		for _, subscription := range matchingSubscriptions(snapshot.WebhookSubscriptions, published.SourceWorkspace(), releaseEvent.Type, published.App) {
			delivery := newWebhookDelivery(releaseEvent, published.SourceWorkspace(), subscription.ID, now)
			snapshot.WebhookDeliveries[delivery.ID] = delivery
		}
		return nil
	})
	return published, err
}

func (s *LocalStore) GetDeployment(ctx context.Context, app string) (contract.Deployment, error) {
	return s.GetDeploymentForWorkspace(ctx, contract.DefaultWorkspace, app)
}

func (s *LocalStore) GetDeploymentForWorkspace(ctx context.Context, workspace string, app string) (contract.Deployment, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return contract.Deployment{}, err
	}
	deployment, ok := snapshot.ReleaseCatalog.Deployments[catalog.DeploymentKey(workspace, app)]
	if !ok {
		return contract.Deployment{}, catalog.ErrDeploymentNotFound
	}
	return deployment, nil
}

func (s *LocalStore) LoadCatalog(ctx context.Context) (catalog.Snapshot, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	return snapshot.ReleaseCatalog, nil
}

func (s *LocalStore) AppendAudit(ctx context.Context, record catalog.AuditRecord) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		record = catalog.PrepareAuditRecord(record, now)
		snapshot.ReleaseCatalog.Audit = append(snapshot.ReleaseCatalog.Audit, record)
		return nil
	})
}

func (s *LocalStore) AuditTrail(ctx context.Context, workspace string, gitSourceID string) ([]catalog.AuditRecord, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]catalog.AuditRecord, 0)
	for _, record := range snapshot.ReleaseCatalog.Audit {
		if record.Workspace == workspace && record.GitSourceID == gitSourceID {
			records = append(records, record)
		}
	}
	return records, nil
}

func (s *LocalStore) SetAppTagOverride(ctx context.Context, workspace string, app string, tagOverride *string) (contract.Deployment, error) {
	var updated contract.Deployment
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		key := catalog.DeploymentKey(workspace, app)
		deployment, ok := snapshot.ReleaseCatalog.Deployments[key]
		if !ok {
			return catalog.ErrDeploymentNotFound
		}
		deployment.TagOverride = cloneCatalogString(tagOverride)
		deployment.UpdatedAt = catalogTimePtr(now)
		snapshot.ReleaseCatalog.Deployments[key] = deployment
		updated = deployment
		return nil
	})
	return updated, err
}

func (s *LocalStore) SetActionTagOverride(ctx context.Context, workspace string, app string, actionKey string, tagOverride *string) (contract.Action, error) {
	var updated contract.Action
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		key := catalog.DeploymentKey(workspace, app)
		deployment, ok := snapshot.ReleaseCatalog.Deployments[key]
		if !ok {
			return catalog.ErrDeploymentNotFound
		}
		action, ok := deployment.Actions[actionKey]
		if !ok {
			return catalog.ErrActionNotFound
		}
		action.TagOverride = cloneCatalogString(tagOverride)
		action.UpdatedAt = catalogTimePtr(now)
		deployment.Actions[actionKey] = action
		snapshot.ReleaseCatalog.Deployments[key] = deployment
		updated = action
		return nil
	})
	return updated, err
}

func (s *LocalStore) ListSourceReleaseMarkers(ctx context.Context) (map[string]catalog.SourceReleaseMarker, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	markers := make(map[string]catalog.SourceReleaseMarker, len(snapshot.ReleaseCatalog.SourceMarkers))
	for key, marker := range snapshot.ReleaseCatalog.SourceMarkers {
		markers[key] = marker
	}
	return markers, nil
}

func (s *LocalStore) ImportCatalog(ctx context.Context, imported catalog.Snapshot) error {
	return s.update(ctx, func(snapshot *Snapshot, _ time.Time) error {
		catalog.MergeSnapshot(&snapshot.ReleaseCatalog, imported)
		return nil
	})
}

func cloneCatalogString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func catalogTimePtr(value time.Time) *time.Time {
	return &value
}
