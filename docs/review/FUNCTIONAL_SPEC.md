# windforce-lite 기능정의서 (Functional Spec) — MECE by 기능 도메인

> **성격**: 코드 리뷰 산출물. windforce 본체와 동일 패턴으로, 발견 순서가 아니라 **기능 도메인 축**으로 top-down 재구성.
> **정체**: windforce의 **경량 재구현** — 같은 개념(source-sync·catalog·ctx-first 실행·큐·HITL)을 **lease 기반**(ping-reaper 아님)으로, **Local(파일 JSON) + Postgres 이중 백엔드**로 구현. 의도적 제외: 스케줄러·multi-tenant quota·billing·operator·flow-composite.
> **기준**: base `81ce543` + branch `review/lite-dual-backend`. 테스트 200개. 짝 문서: [`TEST_CATALOG.md`](./TEST_CATALOG.md).

## 이중 백엔드 = 이 리뷰의 중심 축

windforce는 단일 Postgres였지만 lite는 **Local + Postgres 두 store가 같은 `state.Store` 인터페이스를 구현**한다(Go 컴파일러가 메서드 대칭을 강제). 그래서 lite 고유의 리스크는 **"두 백엔드가 의미론적으로 갈라지는 지점"**이다 — 인터페이스는 대칭이어도 로직이 물리적으로 두 벌인 곳(암호화·reclaim·MaxConcurrent)이 drift 원천.

**리뷰 결론**: 핵심 불변식(materialize 순서·단일 enqueue·at-rest 암호화·claim/complete)은 **두 백엔드 대칭으로 잘 보존**. windforce의 데이터 손상급 결함(G-1 poison·G-2 평문)은 lite에 **없음**. 취약 지대는 **문서↔코드 drift(L-2)·client 키 노출(L-3)·HITL 데이터 at-rest(L-4)·미검증 경로**.

## 도메인 지도 (MECE)

| # | 도메인 | 책임 | 이중백엔드 | 커버리지 |
|---|---|---|---|---|
| L-F1 | Source & Catalog | clone→manifest→materialize→catalog upsert | 번들=파일 단일 | ●●● COVERED |
| L-F2 | Enqueue | `CreateRunAndEnqueue` 단일경로 + 멱등키 | 대칭 | ●●● COVERED |
| L-F3 | Claim & Lease | ClaimJob·lease TTL·heartbeat·만료 reclaim | 대칭(잠금 프로파일 상이) | ●●○ PARTIAL |
| L-F4 | Execution | ctx-first `main(ctx)`·executor·worker | 무관 | ●●● COVERED |
| L-F5 | Completion & Recovery | CompleteJob*·cancel·retry·ExpireStuckJobs | 대칭 | ●●○ PARTIAL |
| L-F6 | HITL (Human Tasks) | waiting-human·resume·task 상태머신 | 대칭 | ●○○ THIN (L-2) |
| L-F7 | Auth & Tenancy (crypto) | 봉투 암호화·token·client 키 | 대칭 | ●●○ PARTIAL |

---

## L-F1. Source & Catalog — ●●● COVERED
git source를 commit 단위로 materialize한 뒤 catalog에 active deployment upsert. deploy는 검증된 git source를 active로 전환.

**불변식**
- **I-10** [원자성] `Materialize`(마커 last, `file.Sync()`) → **그 다음** `Catalog.UpsertDeployment`. 실패 시 upsert 우회 없음(`syncer.go:90-100`, 주석 "Catalog is updated only after fully materialized").
- 번들 저장소는 이중백엔드 무관 — filesystem 단일 구현(`bundle.Store`).

**커버**: `TestSyncMaterializesBeforeCatalogUpdate`, `TestSyncWrapsMaterializeErrorBeforeCatalogUpdate`, `bundle/storage_test.go`. **갭 없음.**

---

## L-F2. Enqueue — ●●● COVERED
모든 run/job 생성이 `execution/service.go`의 `Service.CreateRun` 단일 경로로 수렴 → `store.CreateRunAndEnqueue`. 모든 HTTP 트리거(run·webhook)가 이 한 경로로.

**불변식**
- [단일진실원] job 생성 단일 경로(`CreateRunAndEnqueue` 호출은 service.go 한 곳). windforce I-01 analog 충족.
- [멱등] `deterministicRunID = sha256(ws,app,action,idempotencyKey)[:12]` → `ErrConflict` 시 재조회 `Replayed:true`.
- [암호화] input을 두 백엔드 모두 `encryptInput`으로 at-rest 암호화(대칭 확인).

**커버**: `TestExecutionAPICreatesPinnedRunAndReplaysIdempotencyKey`, `TestPostgresStoreCreateRunAndEnqueueReturnsConflict`, `TestStoreAtRestEncryptionSymmetry`(신규). **갭 없음.**

---

## L-F3. Claim & Lease — ●●○ PARTIAL
`ClaimJob`/`ClaimJobForTags`가 lease(기본 30s)로 job 소유. 워커는 TTL/3 주기 `HeartbeatJob`. 죽은 워커의 lease 만료 시 다른 워커가 재클레임.

**불변식**
- [원자성] claim이 lease_owner+lease_expires_at 설정, `FOR UPDATE SKIP LOCKED`(Postgres)/전역 락(Local).
- [복구] lease 만료(무 heartbeat) → 재큐잉 → 재클레임. cancel+만료 → terminal canceled.
- [정확 상한] MaxConcurrent 게이트(ws+app, running만 집계) — Local/Postgres **두 벌 독립 구현**(drift 잠재, 테스트로 방어).

**★ L-1 (DRIFT/scale)**: Postgres는 **매 `ClaimJobForTags` 호출마다** `UPDATE jobs SET state='queued' WHERE state='running' AND lease_expires_at<now()`를 **LIMIT·워크스페이스 스코프 없이** 실행(`postgres_store.go:401-407`). tx-safe라 windforce G-1의 poison-rollback은 없으나, **한 워커의 클레임이 시스템 전역 lease 정리에 결합**되고 무제한이라 대규모 워커 풀에서 락 경합 증폭. 권고: LIMIT+tag 스코프, 또는 `ExpireStuckJobs`처럼 주기 스윕으로 분리.

**커버**: heartbeat 유지(`exerciseStoreHeartbeatExtendsLease`)·MaxConcurrent(`exerciseStoreMaxConcurrent`)·주기 prune(`exercisePruneSettledJobs`) 대칭 covered. **신규**: `Test{Local,Postgres}StoreExpiredLeaseIsReclaimed`(만료→재클레임, 최상위 갭 박제). **잔여 갭**: L-1 무제한 reclaim 자체의 스케일 동작 미검증.

---

## L-F4. Execution — ●●● COVERED
ctx-first `main(ctx)` 실행(executor·runner·runtime·worker). 언어별 SDK(go/python/typescript).

**불변식**: 실행 계약 = ctx-first, action dispatch. 이중백엔드 무관(실행은 store 밖).

**커버**: executor/runner/runtime/worker 테스트. (환경 주의: `TestRunnerBuildsAndRunsGoApp`는 SDK-go가 Go≥1.25 요구 — 로컬 toolchain 이슈이지 코드 결함 아님.)

---

## L-F5. Completion & Recovery — ●●○ PARTIAL
`CompleteJobSucceeded/Failed/WaitingHuman`가 result를 `encryptJobResult`로 암호화 후 저장(대칭). cancel: queued=즉시종결, running=soft(heartbeat가 kill). `RetryRun`이 failed/canceled/expired를 재큐잉. `ExpireStuckJobs` 주기 정리.

**불변식**
- [원자성] 완료가 result 암호화 + 상태 전이를 단일 tx/락에서.
- [정확성] cancel=해당 job/실행에 대해 terminal(재수령 불가). 단 run은 명시적 `RetryRun`으로 재기동 가능(≠ "절대 불변").
- [멱등] 이미 terminal인 job 재취소 = no-op(`AlreadyCompleted`).

**관찰·갭**
- **L-5** run.Error 평문 저장(두 백엔드, 의도된 에러 메타데이터) — 액션이 에러 메시지에 민감정보를 넣으면 평문 노출 벡터.
- **DRIFT**: `CancelRun`(run 강제취소)이 running job에 `CanceledBy`/구조화 Result를 안 남겨 `CancelJob` 경로와 감사 비대칭.
- **갭**: CancelRun의 running-job 강제취소 후 워커 소유권 상실 시나리오 미검증(양 백엔드).

**커버**: `exerciseStoreLifecycle`(cancel queued/soft/중복무시·retry) 대칭 covered.

---

## L-F6. HITL (Human Tasks) — ●○○ THIN
job이 `CompleteJobWaitingHuman`으로 사람 승인 대기(human_tasks), `ResumeHumanTask`/`ResumeRun`으로 재개. task 상태머신 가드로 멱등.

**불변식**
- [멱등] resume는 `task.State != Pending` / `run.State != WaitingHuman` → `ErrInvalidState`(중복 부작용 방지). windforce의 unique-constraint replay와 달리 **에러 반환식**(호출자가 "이미 처리됨"으로 해석해야).

**★ L-2 (DRIFT, 중대)**: ADR-0002·README가 `POST /v1/human-tasks/{id}/resume`·`/v1/runs/{id}/resume`를 HITL API로 명시하지만 **HTTP 핸들러가 하나도 없음** — 레거시 경로는 `TestLegacyV1ControlPlaneRoutesAreNotExposed`로 404 고정, `readResumeInput`(server.go:495)은 죽은 코드. ResumeHumanTask/ResumeRun은 **Go 라이브러리 import로만 도달 가능**. 문서가 약속한 기능이 미구현. 결정 필요: 구현 vs 문서 정정.

**★ L-4 (at-rest 갭)**: `HumanTask.ResumeInput`(사람 제공 승인 입력)이 **두 백엔드 모두 평문 저장**(local snapshot.HumanTasks / postgres `resume_input` 컬럼). 같은 값이 `job.Payload.Input`으로 병합될 땐 암호화되나 human_tasks 사본은 평문. 대칭이라 divergence는 아니나 실제 at-rest 갭. 결정 필요: 암호화 vs 수용.

**갭**: resume 이중제출 멱등(`ErrInvalidState`) 미검증(양 백엔드). HTTP 진입점 자체 미구현.

---

## L-F7. Auth & Tenancy (crypto) — ●●○ PARTIAL
봉투 암호화(KEK→DEK, `envelope.go`)로 input/result at-rest. token. client 외부키 인증.

**불변식**
- [암호화] AES-256-GCM 봉투, `DeriveKEK`(도메인 분리), `ResolveDEK`(v0 legacy·v1 wrapped·previous KEK). windforce 동일 수준.
- [위조방지] `__wf_enc` 예약어 거부 + `UnwrapEnc`는 DEK 없인 복호 불가.
- [이중백엔드] Local=파생키(`DeriveWorkspaceKey`), Postgres=wrapped DEK(`workspace_key` 테이블, `GetWorkspaceKeyVersioned`) — **키 버저닝/로테이션은 Postgres에만**(의도적 비대칭).

**★ L-3 (보안, windforce G-8/GAP-24 analog)**: `Client.external_key`(인증 비밀)가 **두 백엔드 평문 저장** + `ListClients`/`GetClient` 응답에 **마스킹 없이 노출**(`canonical_clients.go:17`, `state.go:266`). 관리자 토큰으로 워크스페이스 모든 client 평문 키 일괄 조회 가능. `canonical_clients_test.go`가 노출을 명시 고정 = 설계이나 보안 GAP. 결정 필요.

**L-6 (일관성)**: `__wf_enc` 거부 가드가 `readRunInput`(handleJobRun)에만, execution API(`handleExecutionCreateRun`)·webhook엔 없음. 위조 방어 실질은 DEK 요구로 유지되나 진입점 일관성 부족.

**커버**: `TestStoreAtRestEncryptionSymmetry`(신규, 양 백엔드 5경로 물리 바이트 암호문 검증 — Postgres at-rest·resume/waiting-human 경로가 무커버였던 갭 메움). envelope/token 테스트. **갭**: client 키 해싱 부재(설계).

---

## Synthesis & 발견 원장

lite는 **core 불변식을 두 백엔드 대칭으로 잘 보존**한 견고한 경량 재구현이다. windforce의 데이터 손상급 결함은 이식되지 않았다. 발견은 **경계·문서·HITL**에 몰려 있다.

| ID | 도메인 | 성격 | 상태 |
|---|---|---|---|
| **L-1** | L-F3 | 무제한 lease-reclaim이 매 클레임에 인라인(스케일 경합) | 권고(수정 후보) |
| **L-2** | L-F6 | HITL resume HTTP API 문서엔 있으나 미구현(404+죽은코드) | **결정 필요**(구현/정정) |
| **L-3** | L-F7 | client external_key 평문 저장·무마스킹 노출(G-8 analog) | **결정 필요** |
| **L-4** | L-F6 | HumanTask.ResumeInput 평문 at-rest(양 백엔드) | 결정 필요 |
| L-5 | L-F5 | run.Error 평문(의도, 누출 벡터) | 관찰 |
| L-6 | L-F7 | `__wf_enc` 가드 진입점 불일치 | 관찰 |

**신규 회귀테스트**: `TestStoreAtRestEncryptionSymmetry`(양 백엔드 at-rest 물리검증), `Test{Local,Postgres}StoreExpiredLeaseIsReclaimed`(죽은 워커 회수).

**다음 δ**: ① L-2 HITL API 구현/정정 ② L-3 client 키 마스킹+해싱 ③ 미검증 경로 — resume 이중제출·CancelRun running-job·**Postgres HTTP 종단**(server 테스트가 전부 LocalStore 전용) ④ L-1 reclaim bound.
