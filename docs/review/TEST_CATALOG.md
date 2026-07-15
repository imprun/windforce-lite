# windforce-lite 테스트케이스 카탈로그 (분류 리스트)

> 코드 리뷰 산출물. 203개 테스트를 기능 도메인(L-F1~L-F7) 축으로 분류.
> 짝 문서: [`FUNCTIONAL_SPEC.md`](./FUNCTIONAL_SPEC.md). base `81ce543` + `review/lite-dual-backend`.
> **이중 백엔드 표기**: `[L]`=LocalStore, `[P]`=PostgresStore(`WINDFORCE_LITE_POSTGRES_TEST_DSN` 필요), `[L/P]`=공유 exercise 헬퍼로 양쪽.

## 1. 이번 리뷰가 추가한 테스트 (3)

`internal/state/review_characterization_test.go`.

| 테스트 | 도메인 | 검증 |
|---|---|---|
| `TestStoreAtRestEncryptionSymmetry` `[L/P]` | L-F2·F7 | 5개 쓰기경로(Create/WaitingHuman/Resume/Succeeded/Cancel)의 **물리 저장 바이트가 암호문**임을 양 백엔드 직접 검증. Postgres at-rest·resume/waiting-human 무커버 갭 메움. |
| `TestLocalStoreExpiredLeaseIsReclaimed` `[L]` | L-F3 | 죽은 워커의 lease 만료 → 다른 워커 재클레임, 원 워커 소유권 상실. |
| `TestPostgresStoreExpiredLeaseIsReclaimed` `[P]` | L-F3 | 위 시나리오 Postgres 대칭. |

## 2. 도메인별 분류

### L-F1 · Source & Catalog (syncer 13 · source 19 · manifest 20 · bundle/catalog/gitsource)
- **materialize→catalog 순서(I-10)**: `TestSyncMaterializesBeforeCatalogUpdate`, `TestSyncWrapsMaterializeErrorBeforeCatalogUpdate`
- **번들 마커 durability**: `bundle/storage_test.go`
- **manifest 파싱**: manifest 패키지 20 (파싱·검증·거부)
- **source clone/inspect**: source 패키지 19

### L-F2 · Enqueue (execution/service · state)
- **단일경로+멱등키**: `TestExecutionAPICreatesPinnedRunAndReplaysIdempotencyKey`, `TestPostgresStoreCreateRunAndEnqueueReturnsConflict` `[P]`
- **input at-rest 암호화**: `TestLocalStoreEncryptsInputAtRest` `[L]`, `TestStoreAtRestEncryptionSymmetry` `[L/P]` ⟵신규

### L-F3 · Claim & Lease (state · worker)
- **claim/lifecycle**: `exerciseStoreLifecycle` → `TestLocalStoreClaimCompleteAndResumeLifecycle` `[L]` / `TestPostgresStoreClaimCompleteAndResumeLifecycle` `[P]`
- **heartbeat lease 연장**: `exerciseStoreHeartbeatExtendsLease` `[L/P]`
- **MaxConcurrent 게이트**: `exerciseStoreMaxConcurrent` → `TestLocalStoreClaimJobEnforcesMaxConcurrent` `[L]` / `TestPostgresStore…` `[P]`
- **주기 prune**: `exercisePruneSettledJobs` `[L/P]`
- **lease 만료→재클레임**: `Test{Local,Postgres}StoreExpiredLeaseIsReclaimed` `[L/P]` ⟵신규
- **worker 소비·하트비트**: worker 패키지 9

### L-F4 · Execution (executor 8 · runner 4 · runtime 18 · sdk/go 11)
- **ctx-first 실행·dispatch**: executor/runner/runtime 패키지
- ⚠ `TestRunnerBuildsAndRunsGoApp`: SDK-go가 Go≥1.25 요구 — 로컬 toolchain 이슈(코드 아님)

### L-F5 · Completion & Recovery (state)
- **complete/cancel/retry**: `exerciseStoreLifecycle`(succeeded/failed/cancel queued·soft/중복무시/retry) `[L/P]`
- **result at-rest 암호화**: `TestLocalStoreEncryptsResultAtRest` `[L]`, `TestStoreAtRestEncryptionSymmetry` `[L/P]` ⟵신규
- **ExpireStuckJobs(주기 reaper)**: `exercisePruneSettledJobs` `[L/P]`

### L-F6 · HITL (state)
- **resume 단발**: `exerciseStoreLifecycle` 내 resume, `worker_test.go` resume
- (진입점: `TestLegacyV1ControlPlaneRoutesAreNotExposed` — /v1 resume 경로 404 고정)

### L-F7 · Auth & Tenancy (crypto 2 · token 1 · server clients)
- **봉투 암호화**: crypto 패키지, `TestStoreAtRestEncryptionSymmetry` ⟵신규
- **__wf_enc 거부**: `server_test.go`(readRunInput 경로)
- **client CRUD/노출**: `clients_test.go` `[L/P]`(state), `canonical_clients_test.go` `[L]`(HTTP)

### 서버 HTTP (server 56)
- run/webhook/deploy/catalog/history/clients API. **주의: 전부 LocalStore 전용**(`OpenPostgresStore` 사용 0건) — Postgres HTTP 종단 미검증.

---

## 3. 커버리지 갭 원장 (다음 δ)

| 우선 | 도메인 | 미커버 (있어야 할 테스트) | 근거 |
|---|---|---|---|
| 1 | L-F6 | **HITL resume HTTP 진입점 자체 미구현** (L-2) — 구현하거나 ADR/README 정정 | 핸들러 0건, /v1 404 고정, readResumeInput 죽은코드 |
| 1 | L-F7 | client external_key 마스킹/해싱 (L-3) — 평문 저장·노출 | `canonical_clients.go:17`, `state.go:266` |
| 2 | L-F6 | resume 이중제출 멱등(`ErrInvalidState`) — 양 백엔드 | 단발 호출만 존재 |
| 2 | L-F5 | CancelRun의 **running-job 강제취소 후 소유권 상실** — 양 백엔드 | 테스트의 CancelRun은 queued 대상만 |
| 2 | 서버 | **Postgres HTTP 종단** — RequeueQueuedJobsForApp·Client API·CancelRun | server 테스트 전부 LocalStore |
| 3 | L-F3 | L-1 무제한 lease-reclaim 스케일 동작 | LIMIT/스코프 없는 인라인 UPDATE |
| 3 | L-F6 | HumanTask.ResumeInput at-rest 암호화(L-4) 여부 결정 후 박제 | 현재 평문 |

## 4. 실행

```
docker exec <pg> psql -U test -c "CREATE DATABASE lite_test;"   # 1회
WINDFORCE_LITE_POSTGRES_TEST_DSN=postgres://test:test@host:port/lite_test?sslmode=disable \
  go test ./internal/state/... ./internal/syncer/... ./internal/server/...
```

리뷰 신규만: `go test ./internal/state -run 'StoreAtRestEncryptionSymmetry|ExpiredLeaseIsReclaimed' -count=1`
DSN 없으면 Postgres 테스트는 자동 skip(Local만 실행). runtime Go-app 빌드 테스트는 Go≥1.25 toolchain 필요.
