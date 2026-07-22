# ADR 0012: 실행 역할을 server, worker, standalone으로 구성

## Status

Accepted (2026-07-22) — issue #126.

이 결정은 ADR 0008의 Dispatcher 프로세스 배치와 ADR 0011의 Public API 프로세스 배치를 갱신한다. 각 ADR의 Webhook 전달 계약과 Public API 계약은 그대로 유지한다.

## Context

Control API, Execution API, Public API와 Worker API는 서로 다른 신뢰 집단과 버전 계약을 가진다. 이 HTTP plane 구분은 유효하지만 각 plane을 별도 프로세스 역할로 배치하면 모든 새 표면마다 기동 위치, 설정 조합, dispatcher 소유권을 다시 결정해야 한다.

Webhook delivery는 lease와 PostgreSQL row lock으로 동시 실행을 조정한다. 따라서 모든 HTTP server replica가 dispatcher loop를 실행해도 동일 delivery를 동시에 활성 claim하지 않는다. In-tree 어댑터도 admission 패키지를 직접 호출할 수 있으므로 같은 프로세스 안에서 Execution API 네트워크 홉을 사용할 이유가 없다.

## Decision

1. **세 가지 실행 역할.** 실행 명령은 `server`, `worker`, `standalone`을 문서화한다. `standalone`은 server와 worker를 한 프로세스에서 실행한다.
2. **server가 모든 HTTP plane을 제공한다.** Control `/api/w`, trusted execution `/execution/v1`, public `/api/v1`, worker `/worker/v1`, Web UI와 `/metrics`를 한 listener에서 제공한다. HTTP plane별 enable 설정은 두지 않는다.
3. **server가 background loop를 소유한다.** 모든 server와 standalone replica가 Webhook Dispatcher, Webhook retention과 Job retention을 실행한다. Dispatcher lease와 row lock이 replica 간 claim을 조정한다.
4. **어댑터 경계는 admission 서비스다.** In-tree 어댑터는 `execution.Service.CreateRun`을 in-process로 호출한다. 프로세스 밖 어댑터는 `/execution/v1` SDK를 사용한다. 두 전송은 `CreateRunRequest`와 replay 의미를 동일하게 유지한다.
5. **명령 alias.** `api`, `control-plane`, `execution-api` 명령은 `server`로 해석하지만 도움말과 운영 문서에는 노출하지 않는다. Webhook Dispatcher는 독립 역할과 명령을 갖지 않는다.
6. **URL 계약은 배치와 독립적이다.** `/api/w`, `/execution/v1`, `/api/v1`, `/worker/v1` 경로와 인증 경계는 유지한다.

## Consequences

- self-hoster는 server와 worker를 독립 확장하거나 standalone 하나를 실행한다.
- server replica 증설은 HTTP 처리량과 Webhook delivery claim 참여자를 함께 늘린다.
- 원격 worker는 server의 `/worker/v1`과 job callback API에만 접근하며 PostgreSQL을 노출할 필요가 없다.
- `/execution/v1`을 `/api/v1`로 흡수하는 작업은 별도 API·SDK 결정으로 다룬다.
