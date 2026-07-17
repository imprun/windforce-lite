# ADR 0009: 라벨 기반 워커 매칭 프로토콜

## Status

Accepted (2026-07-17) — 설계 확정, 구현 전. 논의: issue #81.

## Context

현재 잡→워커 라우팅은 capability 화이트리스트로 동작한다. `windforce.json`의
`requiredCapabilities`는 `capabilityRouteTags` 맵(현재 `browser` 하나)을 거쳐
잡당 **단일 route tag**로 변환되고, 워커는 태그 집합을 들고 claim하며, 매칭은
"잡의 태그가 워커 태그 집합에 포함되는가"다. 태그가 없는 워커는 모든 잡을
claim한다.

이 구조의 한계:

- 새 실행 환경(아키텍처, 지역, 커스텀 런타임, GPU 등)을 추가할 때마다 엔진
  코드의 화이트리스트를 수정해야 한다. 라우팅 어휘가 엔진 소유라 오퍼레이터가
  자기 풀을 자기 어휘로 구성할 수 없다.
- 잡당 태그가 하나뿐이라 "browser이면서 특정 지역" 같은 조합 요구를 표현할 수
  없다.
- 태그 없는 워커가 전부 claim하는 기본값은 능력 없는 워커가 browser 잡을 집어
  실패시키는 사고를 허용한다.
- 워커가 등록·heartbeat 없이 익명으로 폴링하므로 "지금 어떤 능력의 워커가 몇
  개 살아 있는가"를 관측할 정본이 없다.

셀프호스터는 이질적 워커 풀(예: 일반 리눅스 + 브라우저 박스 + arm 빌더)을
자기 라벨로 선언하고, 앱이 필요한 워커를 manifest에서 요구하는 것만으로
라우팅이 완결되어야 한다.

## Decision

1. **라벨 어휘 (개방).** label은 `[a-z0-9]([a-z0-9._-]{0,62}[a-z0-9])?` 형식의
   소문자 토큰이며, 잡과 워커 각각 최대 16개. 화이트리스트는 폐지하고 형식
   검증만 엔진이 소유한다.
2. **요구 선언.** `windforce.json`에 `runsOn: ["label", ...]`을 도입한다. app
   수준과 action 수준 모두 선언 가능하며 유효 요구는 두 집합의 합집합이다.
   `requiredCapabilities`는 `runsOn`의 별칭으로 동작하고(`browser` →
   라벨 `browser`) 문서에서 deprecated로 안내한다.
3. **enqueue 시 self-pin.** 유효 요구 라벨 집합은 enqueue 시점에 잡에
   핀된다. 이후의 재sync·재배포는 이미 큐에 있는 잡의 라우팅을 바꾸지 않는다
   (기존 self-pin 불변식의 확장).
4. **매칭 의미론 (subset containment).** 워커는 자신의 라벨 집합이 잡의 요구
   라벨 집합을 **포함**할 때만 그 잡을 claim할 수 있다. 요구가 빈 잡은 모든
   워커가 claim할 수 있다. **라벨이 빈 워커는 요구가 빈 잡만 claim한다** —
   "태그 없는 워커가 전부 claim"하던 기존 동작의 의도적 변경이며, 업그레이드
   노트에 명시한다.
5. **시스템 라벨 네임스페이스.** `sys/` 프리픽스 라벨은 예약이다. manifest의
   `runsOn`에 나타나면 검증 에러로 거부하고, 워커 기동 구성과 서버 정책만
   부여할 수 있다. 오퍼레이터가 배치 차원(예: `sys/pool.dedicated-a`)을 저자
   입력과 격리하는 용도다.
6. **워커 등록·heartbeat.** 워커는 기동 시 등록(id, labels, slots,
   started_at)하고 주기적으로 heartbeat하며 정상 종료 시 해제한다. 등록부는
   "지금 살아 있는 능력"의 관측 정본이고 list-workers API로 노출한다.
   `slots`는 워커의 동시 실행 상한(정량)으로 라벨(정성)과 구분한다. 잡 단위
   자원 요구(cpu/mem)는 이 ADR 범위 밖이다.
7. **저장 모델.** 잡은 요구 라벨 집합을 저장하고(`required_labels`), 워커
   등록부는 별도 테이블/컬렉션으로 둔다. Postgres claim은 기존
   `FOR UPDATE SKIP LOCKED` 순서를 유지한 채 라벨 containment 필터를
   추가한다. 로컬 JSON 백엔드도 동일 의미론을 구현한다.

## Consequences

- 새 실행 환경 추가에 엔진 코드 수정이 필요 없어진다 — 라벨은 오퍼레이터의
  어휘가 된다.
- 라벨 없는 워커의 claim 범위가 좁아진다(요구 없는 잡만). 기존 배포는
  업그레이드 시 워커에 라벨을 부여하거나 앱의 요구를 비워야 한다.
- `capabilityRouteTags` 화이트리스트와 단일 route tag는 라벨 검증과 라벨
  집합으로 대체된다.
- 원격 워커가 DB 대신 HTTP로 등록·claim·heartbeat하는 표면은 이 ADR 범위
  밖이며 후속 ADR 후보로 남긴다.

## Amendment (2026-07-18) — 명시 route tag와의 공존 확정

구현(#84)에서 확정한 사항:

- 명시 `tag`(릴리스 라우팅)와 라벨(능력 요구)은 **직교하는 두 claim 차원**이다. 잡은 둘 다 가질 수 있고 claim은 둘 다 만족해야 한다. 이에 따라 manifest의 tag×capabilities 상호배제 규칙과 admission의 routing-conflict 검사는 제거되었다.
- tag 차원은 기존 의미론을 유지한다(태그 없는 워커 = 모든 태그). 라벨 차원만 subset containment(라벨 없는 워커 = 요구 없는 잡만)를 따른다.
- capabilities는 더 이상 route tag를 합성하지 않는다. **업그레이드 노트**: capability 풀 워커는 `--tags browser` 대신 `--labels browser`로 전환한다. 이미 큐에 있는 구 잡은 requiredCapabilities를 라벨 요구로 읽는 폴백으로 매칭된다.
