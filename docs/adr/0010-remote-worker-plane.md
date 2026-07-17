# ADR 0010: 원격 워커 플레인 — HTTP 등록·claim·완료 표면

## Status

Accepted (2026-07-18) — 서버 표면 구현과 함께 확정. 논의: ADR 0009의 후속(원격 claim 표면), issue #84의 연장.

## Context

워커는 state store 직결(claim CTE/스냅샷)로만 잡을 집을 수 있어 엔진 프로세스와 같은 네트워크·디스크 안에서만 산다. 셀프호스터가 워커를 다른 머신(빌드 박스, 사내망, 고객 인프라)에서 붙이려면 DB 노출 없이 동작하는 HTTP 표면이 필요하다. 잡 실행 중의 ctx 콜백(state/variables/resources)은 이미 HTTP(WF_API_URL + job 토큰)라서, 남은 것은 claim 플레인이다.

Processor가 실제로 사용하는 store 표면은 좁다: 워커 등록 3종, claim, 잡 heartbeat, 완료 3종, 로그 append, 그리고 입력 준비(DecryptInput·ResolveInput)와 실행 번들 획득이다.

## Decision

1. **경로와 인증.** 원격 워커 플레인은 `/worker/v1/*`로 버저닝한다. 인증은 워커 토큰(`--worker-token-env`, 미설정 시 admin 토큰)의 `Authorization: Bearer`다. 게이트웨이/프록시 뒤에 두는 배치에서는 프록시가 자체 크레덴셜을 교체 주입하는 패턴을 그대로 쓸 수 있다.
2. **입력 준비는 서버가 한다(prepared claim).** `POST /worker/v1/claims`는 claim 후 서버 안에서 DecryptInput·ResolveInput까지 마친 잡을 돌려준다. `SECRET_KEY`와 복호화는 엔진 프로세스를 떠나지 않고, 준비 실패는 서버가 그 자리에서 잡을 실패 처리한 뒤 204를 돌려준다(워커는 다음 폴에서 재시도).
3. **표면.**
   - `POST /worker/v1/workers` — 등록 {id?, group, tags, labels, slots} → {id}
   - `POST /worker/v1/workers/{id}/heartbeat`, `DELETE /worker/v1/workers/{id}`
   - `POST /worker/v1/claims` {worker_id, tags, labels, lease_ttl_ms} → 200 {job, lease} | 204(잡 없음)
   - `POST /worker/v1/jobs/{id}/heartbeat` {lease} → {still_owned, canceled_by?, canceled_reason?}
   - `POST /worker/v1/jobs/{id}/complete` {lease, outcome: succeeded|failed|waiting_human, result, human_task?}
   - `POST /worker/v1/jobs/{id}/logs` {workspace, chunk}
   - `GET /worker/v1/artifacts/{digest}` — 실행 번들을 tar 스트림으로. 소스 번들은 서빙하지 않는다: admission이 실행 번들 없는 릴리스를 거부하므로 원격 워커는 digest 기반 실행 번들만으로 완결된다.
4. **매칭 의미론은 로컬과 동일하다.** claim 요청의 tags/labels는 ADR 0009의 이중 차원(태그 멤버십 + 라벨 subset containment)을 그대로 따른다. 원격이라고 다른 규칙은 없다.
5. **와이어 형식.** 요청·응답 필드는 snake_case JSON이며 lease는 {job_id, worker_id, attempt, acquired_at, expires_at}로 직렬화한다. 이 표면은 공개 계약으로, 변경은 새 버전 경로로 한다.

## Consequences

- 워커 바이너리는 `--api-url`(+토큰)로 store 직결 대신 이 표면을 쓰는 백엔드를 선택할 수 있게 된다(클라이언트 구현은 후속 슬라이스).
- 잡 로그·완료가 HTTP 홉을 거치므로 원격 워커의 지연·재시도 특성은 로컬 워커와 다르다. lease TTL과 heartbeat가 그 안전망이다.
- DB·번들 디렉터리·SECRET_KEY는 어떤 형태로도 워커 쪽에 노출되지 않는다.
