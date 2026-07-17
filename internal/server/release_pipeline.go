package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	gitsourcepkg "github.com/imprun/windforce-core/internal/gitsource"
	"github.com/imprun/windforce-core/internal/syncer"
)

type gitSourceOperationAudit struct {
	Source       string
	DeploymentID *string
	Message      *string
	CreatedBy    *string
}

const sourceOperationLeaseTTL = 2 * time.Minute

var errGitSourceOperationBusy = errors.New("git source operation already in progress")

type CandidatePreparer interface {
	Prepare(ctx context.Context, deployment contract.Deployment) (string, error)
}

func (h *Handler) stageGitSourceCandidate(w http.ResponseWriter, r *http.Request, workspaceID string, source gitsourcepkg.Source) (catalog.ReleaseCandidate, bool) {
	operationCtx, release, err := h.acquireGitSourceOperation(r.Context(), workspaceID, source)
	if err != nil {
		writeSourceOperationError(w, err)
		return catalog.ReleaseCandidate{}, false
	}
	defer release()

	token, err := h.resolveGitSourceCreds(operationCtx, workspaceID, source.TokenEnv)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return catalog.ReleaseCandidate{}, false
	}
	s := *h.syncer
	deployment, err := s.Sync(operationCtx, syncer.Source{
		Workspace:   workspaceID,
		GitSourceID: source.ID,
		RepoURL:     source.RepoURL,
		Branch:      source.Branch,
		Subpath:     source.Subpath,
		Token:       token,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return catalog.ReleaseCandidate{}, false
	}
	if h.candidatePreparer == nil {
		writeError(w, http.StatusServiceUnavailable, "release candidate runtime preparer is not configured")
		return catalog.ReleaseCandidate{}, false
	}
	if _, err := h.candidatePreparer.Prepare(operationCtx, deployment); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "release candidate preparation failed: "+err.Error())
		return catalog.ReleaseCandidate{}, false
	}
	candidateStore, ok := h.catalog.(catalog.ReleaseCandidateStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "release candidate store is not configured")
		return catalog.ReleaseCandidate{}, false
	}
	candidate, err := candidateStore.SaveReleaseCandidate(operationCtx, deployment, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return catalog.ReleaseCandidate{}, false
	}
	if marker, ok := h.gitSources.(interface {
		MarkSynced(context.Context, string, string, string, time.Time) (gitsourcepkg.Source, error)
	}); ok {
		if _, err := marker.MarkSynced(operationCtx, workspaceID, source.ID, candidate.Deployment.Commit, candidate.SyncedAt); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return catalog.ReleaseCandidate{}, false
		}
	}
	return candidate, true
}

func (h *Handler) publishGitSourceCandidate(w http.ResponseWriter, r *http.Request, workspaceID string, source gitsourcepkg.Source, candidate catalog.ReleaseCandidate, audit gitSourceOperationAudit) (contract.Deployment, bool) {
	operationCtx, release, err := h.acquireGitSourceOperation(r.Context(), workspaceID, source)
	if err != nil {
		writeSourceOperationError(w, err)
		return contract.Deployment{}, false
	}
	defer release()
	if err := catalog.ValidateReleaseCandidate(candidate, workspaceID, source.ID, candidate.Deployment.Commit); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return contract.Deployment{}, false
	}
	deployment := candidate.Deployment
	deployment.Source = strings.TrimSpace(audit.Source)
	deployment.DeploymentID = cloneStringPtr(audit.DeploymentID)
	deployment.CreatedBy = cloneStringPtr(audit.CreatedBy)
	if audit.Message != nil {
		deployment.Message = cloneStringPtr(audit.Message)
	}
	releasedAt := time.Now().UTC()
	deployment.UpdatedAt = &releasedAt
	for key, action := range deployment.Actions {
		action.UpdatedAt = &releasedAt
		deployment.Actions[key] = action
	}
	publisher, ok := h.catalog.(interface {
		PublishRelease(context.Context, contract.Deployment, time.Time) (contract.Deployment, error)
	})
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "transactional release catalog is not configured")
		return contract.Deployment{}, false
	}
	deployment, err = publisher.PublishRelease(operationCtx, deployment, releasedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return contract.Deployment{}, false
	}
	return deployment, true
}

func writeSourceOperationError(w http.ResponseWriter, err error) {
	if errors.Is(err, errGitSourceOperationBusy) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func (h *Handler) acquireGitSourceOperation(ctx context.Context, workspaceID string, source gitsourcepkg.Source) (context.Context, func(), error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	sourceID := strings.TrimSpace(source.ID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(source.Name)
	}
	key := workspaceID + "\x00" + sourceID
	value, _ := h.syncLocks.LoadOrStore(key, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	if !lock.TryLock() {
		return nil, nil, errGitSourceOperationBusy
	}
	leaser, distributed := h.catalog.(catalog.SourceOperationLeaseStore)
	if !distributed {
		return ctx, lock.Unlock, nil
	}
	holder := newDeploymentOperationID()
	acquired, err := leaser.AcquireSourceOperationLease(ctx, workspaceID, sourceID, holder, sourceOperationLeaseTTL)
	if err != nil {
		lock.Unlock()
		return nil, nil, err
	}
	if !acquired {
		lock.Unlock()
		return nil, nil, errGitSourceOperationBusy
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(sourceOperationLeaseTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				renewed, renewErr := leaser.RenewSourceOperationLease(leaseCtx, workspaceID, sourceID, holder, sourceOperationLeaseTTL)
				if renewErr != nil || !renewed {
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			<-done
			releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer releaseCancel()
			_ = leaser.ReleaseSourceOperationLease(releaseCtx, workspaceID, sourceID, holder)
			lock.Unlock()
		})
	}
	return leaseCtx, release, nil
}
