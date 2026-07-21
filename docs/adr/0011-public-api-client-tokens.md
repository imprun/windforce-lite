# ADR 0011: Client 토큰 기반 Public API Plane

## Status

Accepted (2026-07-22) — issue #125.

## Context

Client Registry와 InputConfig는 workspace 안의 외부 호출자 정체성과 호출자별 입력 정책을 표현하지만, 외부 호출자가 자신의 자격증명으로 Action을 실행하는 HTTP 표면이 없었다. `external_key`를 평문 식별자로 저장하고 신뢰된 어댑터가 이를 전달하는 방식은 공개 자격증명 수명주기, 게이트웨이의 bearer 분류, 회전·revoke, 공개 트래픽 보호를 제공하지 못한다.

공개 호출 표면은 control plane과 인증 경계를 공유하면 안 된다. 또한 Run/Job 생성 규칙을 별도로 구현하면 Execution Plane의 admission, 릴리스 pinning, idempotency, InputConfig 해석과 달라질 수 있다.

## Decision

1. **별도 Public API Plane.** 외부 호출 경로는 `/api/v1/w/{workspace}/run/{app}/{action}`와 `/api/v1/w/{workspace}/run/{app}/{action}/wait`로 둔다. `/api/v1/`은 control plane `/api/w/`보다 먼저 라우팅하며 client bearer만 인증한다.
2. **단일 활성 client 토큰.** Client는 `wfk_` 토큰 하나를 활성 자격증명으로 가진다. 생성·회전 응답에서 원문을 한 번만 보여주고 state에는 SHA-256 hash만 저장한다. 회전과 revoke는 즉시 실효하며 활성 토큰이 있는 Client 삭제는 거부한다. 여러 토큰과 토큰별 이름은 별도 계약으로 다룬다.
3. **wf bearer 계열.** `wfk_`를 `contract.CellBearerTokenPrefixes()`에 포함한다. 프론팅 플랫폼은 이 prefix를 엔진 검증 bearer로 분류해 credential swap 없이 전달한다.
4. **Admission 재사용.** Public API handler는 client를 인증하고 `execution.Service.CreateRun`만 호출한다. 큐와 카탈로그에 직접 쓰지 않는다. 생성된 Run/Job은 `ClientID`와 `TriggerKind=http`를 가진다.
5. **InputConfig와 LockedKeys.** app 전체 → action 전체 → client app → client action 순으로 설정을 겹친 뒤, 잠기지 않은 요청 필드를 적용한다. 호출자가 locked key를 보내면 admission에서 400으로 거부한다. 병합된 입력은 active release의 input schema로 검증하고 Run/Job에 고정하며 worker에서 다시 해석하지 않는다.
6. **명세 정본.** action 요청·응답 명세는 active release의 `windforce.json`과 companion JSON Schema다. client가 쓸 수 있는 입력 계약은 manifest input schema에서 유효 LockedKeys를 제외한 형태이며, 별도 공개 schema 사본을 두지 않는다.
7. **동기·비동기 응답.** 비동기는 201과 `job_id`, wait는 완료 시 원시 JSON 결과를 200으로 반환한다. admission 이후 모든 응답에 `X-WF-Job-Id`를 넣는다. 멱등 재호출은 최초 Job ID와 결과를 재사용한다. 실행 실패는 HTTP transport 실패가 아니라 실행 결과다.
8. **공개 표면 보호와 감사.** 인스턴스 전역 token-bucket limiter를 인증 앞단에 두고 `--public-api-rps`, `--public-api-burst`로 설정한다. 인증 실패는 제시된 토큰을 남기지 않고 workspace 감사로 기록하며, 인증된 admission 성공·거부는 client/app/action/Job ID/replay 여부를 Client Audit에 남긴다.
9. **자격증명 형식 전환.** `wfk_`가 아닌 client credential은 공개 bearer로 승격하지 않고 revoke한다. 운영자는 control plane에서 새 토큰을 발급한다. 마이그레이션은 한 번만 적용하고 이후 발급된 `wfk_` 토큰 hash를 보존한다.
10. **프로세스 배치.** `standalone`과 `execution-api`가 Public API Plane을 기동한다. `standalone`은 추가 enable 설정 없이 worker와 함께 동작한다.

## Consequences

- self-hoster는 별도 gateway adapter 없이 client별 Action API를 제공할 수 있다.
- client 토큰은 control plane과 다른 최소 권한 자격증명이므로 token 유출의 영향 범위가 한 workspace의 Public API 호출로 제한된다.
- client 생성·회전 호출자는 한 번 표시되는 토큰을 안전한 호출 시스템에 전달해야 한다.
- 프론팅 플랫폼은 `wfk_` pass-through와 `/api/v1/` 계량 규칙을 별도 통합 작업으로 반영해야 한다.
