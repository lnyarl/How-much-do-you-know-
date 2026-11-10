# Penumbra 페넘브라 개인 지식 기록

기존 AI가 스킬을 따라 로컬 CLI를 호출하고, Go와 SQLite 코어가 개인의 지식 표현과 근거를 저장한다. 타입이 있는 온톨로지와 사용자의 관찰 기록을 분리한다.

사용자가 표현한 내용을 보존하며 올바름이나 이해도는 판정하지 않는다. 관련 개념을 안다고 추론하거나 시간 경과로 망각을 생성하지 않는다. 향후 올바름 관리 엔진은 관찰과 별도의 계약으로 연결한다.

## 실행 환경

Go 1.26 이상을 사용한다. SQLite 드라이버는 순수 Go 구현인 modernc.org/sqlite v1.60.1이다. 별도 SQLite 서버나 CGO 설정은 필요하지 않다.

## AI와 사용하기

저장소 루트의 터미널에서 실행한다.

```sh
go build -o bin/penu ./cmd/penu
./bin/penu init
```

init은 빈 개인 DB를 만들고 JSON으로 실제 저장 경로를 출력한다. 기본 경로는 OS 사용자 설정 디렉터리 아래 penumbra/knowledge.db다. PENU_DB_PATH 또는 명령 앞의 --db로 다른 경로를 지정할 수 있다.

이전 이름으로 생성한 a/knowledge.db가 있고 새 기본 DB가 없으면 기존 DB를 그대로 사용한다. 기존 A_DB_PATH 설정도 지원하며 PENU_DB_PATH가 우선한다. 이름 변경으로 DB를 옮기거나 덮어쓰지 않는다.

그다음 이 저장소에서 파일과 셸을 사용할 수 있는 AI에게 아래처럼 요청한다.

> skills/penumbra/SKILL.md를 읽고 ./bin/penu를 사용해 내 데이터베이스 지식을 정리하자. 한 번에 하나씩 질문하고 내 실제 발언만 기록해줘.

이미 지정한 소스가 있다면 함께 알려준다. AI는 기존 기록을 조회하고 질문한 뒤 실제 사용자 발언을 저장한다. 학습 설명은 제공된 소스에서 확인한 내용으로 진행한다.

다른 대화에서 같은 기록을 사용하려면 같은 스킬과 DB 경로를 지정한다.

> skills/penumbra/SKILL.md를 읽고 Penumbra에 기록된 내 표현에서 출발해 트랜잭션을 설명해줘. 대화에서 직접 확인된 새 발언도 기록해줘.

스킬은 [skills/penumbra](skills/penumbra/SKILL.md)에 있다. CLI가 자체적으로 LLM을 호출하지는 않는다. 스킬을 읽고 셸을 호출할 수 있는 기존 AI가 대화를 담당한다. 자동 스킬 검색을 사용하려면 해당 AI의 스킬 설치 위치에 penumbra 폴더 전체를 복사한다.

저장소 밖에서 CLI를 쓰려면 go install ./cmd/penu로 설치하고 해당 Go 실행 경로를 PATH에 추가한다. 스킬은 PENU_CLI로 지정한 실행 파일, 현재 저장소의 ./bin/penu, PATH의 penu 순으로 찾는다. 사용자가 선택한 DB는 주제가 바뀌어도 유지한다.

## 직접 조회하기

```sh
./bin/penu concepts search '트랜잭션'
./bin/penu context get --concept db:transaction
./bin/penu evidence get observation-001
./bin/penu concepts sources --concept db:transaction
./bin/penu sources get --version source:database:v1
```

ID는 검색과 등록 결과의 실제 값을 사용한다. init 직후에는 개념과 관찰이 없다. 이 명령의 예제 ID가 자동으로 등록되지는 않는다. 전체 명령은 ./bin/penu --help, 개별 옵션은 ./bin/penu context get --help로 확인한다.

성공은 stdout의 JSON, 오류는 stderr의 JSON이다. --db는 전역 옵션이므로 ./bin/penu --db /path/knowledge.db context get --concept db:transaction처럼 명령 앞에 둔다. concepts search의 옵션도 검색어 앞에 둔다.

## 별도 DB에서 데모 실행하기

아래 JSON은 가상 사용자 발언이다. 개인 DB에 넣지 않고 별도 임시 DB에서만 실행한다.

```sh
DEMO_DB_DIR="$(mktemp -d)"
./bin/penu --db "$DEMO_DB_DIR/demo.db" init
./bin/penu --db "$DEMO_DB_DIR/demo.db" concepts register --file examples/ontology.json
./bin/penu --db "$DEMO_DB_DIR/demo.db" feedback submit --file examples/feedback.json
./bin/penu --db "$DEMO_DB_DIR/demo.db" context get --concept demo-db:transaction --concept demo-db:isolation
./bin/penu --db "$DEMO_DB_DIR/demo.db" feedback submit --file examples/forgotten.json
./bin/penu --db "$DEMO_DB_DIR/demo.db" context get --concept demo-db:isolation
```

첫 조회는 트랜잭션에 대한 직접 관찰과 격리 수준의 미확인을 구분한다. 마지막 조회는 격리 수준에 대한 직접 망각 보고를 signals로 반환한다. 관계가 있다는 이유로 이해를 추론하지 않는다.

## 검증

```sh
go test ./...
go test -race ./...
go vet ./...
```

## Go 라이브러리에서 사용하기

외부 Go 호출자는 `penumbra/engine`을 사용한다.

1. `engine.Open(ctx, path)`으로 로컬 DB를 연다. 디렉터리는 미리 준비한다.
2. `RegisterOntology`로 개념과 소스에 근거한 관계를 등록한다.
3. `SubmitFeedback`으로 사용자 원문과 직접 관찰을 함께 제출한다.
4. `GetContext`로 요청한 개념의 관찰, 자기보고, 미확인 항목을 조회한다.
5. `GetEvidence`로 원문, 도움 조건, 후보 진술, 정정 이력을 확인한다.
6. `AmendRecord`로 잘못된 추출이나 연결을 철회하거나 기존 등록 기록으로 대체한다.

전체 호출 예제는 [실행 가능한 Go 예제](engine/example_test.go)에 있다.

```sh
go test ./engine -run ExampleEngine -v
```

출력은 자기보고 하나와 미확인 개념을 구분한다.

```text
observations=1
kind=user_claims_familiarity
unobserved=[cs:thread]
```

## 인터페이스

| 메서드 | 역할 |
| --- | --- |
| `RegisterOntology` | 개념, 별칭, 소스 버전, 근거 관계 등록 |
| `SubmitFeedback` | 대화 원문과 관찰, 후보 진술 저장 |
| `SearchConcepts` | 이름과 별칭의 정확 일치 및 부분 문자열 검색 |
| `GetContext` | 직접 연결된 관찰만 예산 안에서 조회 |
| `GetEvidence` | 관찰의 원문과 이력 조회 |
| `ExploreOntology` | 방향과 깊이를 제한해 개념 관계 탐색 |
| `GetConceptSources` | 개념의 소스 구간 조회 |
| `GetSourceVersion` | 해당 버전의 원문 조회 |
| `AmendRecord` | 기록 철회 또는 기존 기록으로 대체 |
| `Metadata` | profile, 스키마, 온톨로지, 데이터 revision 조회 |
| `Close` | DB 연결 종료 |

JSON을 받는 어댑터는 `engine.DecodeStrict[engine.FeedbackRequest](data)` 같은 진입점을 사용한다. 알 수 없는 필드, 중복 JSON 키, 여러 JSON 값은 거부한다. Go 구조체 호출도 같은 검증과 트랜잭션을 거친다.

## 기록과 조회 규칙

- DB 하나는 개인 하나의 profile을 가진다. producer는 발언자 신분이 아니라 제출한 도구의 출처다.
- 메시지와 관찰은 원문을 덮어쓰지 않는다. 부분 인용은 UTF-8 바이트 구간이 필요하다.
- 모호한 개념 연결은 `candidate`로 저장하며 기본 조회에 포함하지 않는다.
- `user_confirmed` 연결에는 대상 개념을 명시한 사용자 확인 발언이 필요하다. 단순한 "네"만으로 대상을 확정하지 않는다.
- 개인 진술의 AI 해석은 `candidate`다. 사용자 확인으로 표시하려면 진술 내용을 명시한 확인 발언이 필요하다.
- 망각과 회상은 직접 보고된 사건으로 반환한다. 상반된 보고도 시점과 함께 보존한다.
- `GetEvidence`는 철회된 기록과 후보도 포함하는 이력 조회다. `Active`와 `Amendments`를 함께 확인한다.
- `GetContext`의 미확인 항목은 전체 유효 관찰을 기준으로 계산한다. 출력 예산으로 원문이 생략돼도 미확인으로 바꾸지 않는다.
- 한 항목이 바이트 예산보다 크면 ID를 `omitted_evidence`로 반환한다. `GetEvidence`로 전체 원문을 조회할 수 있다.
- 페이지 cursor는 DB와 쿼리, revision에 묶인다. 데이터가 바뀌면 `STALE_CURSOR`를 받고 첫 페이지부터 다시 조회한다.
- 쓰기는 묶음 전체가 성공하거나 롤백된다. 같은 producer와 request ID의 재전송은 같은 결과를 반환한다.
- ID에 다른 내용을 넣거나 같은 request ID를 다른 작업에 재사용하면 충돌이다. 조회는 데이터를 변경하지 않는다.
- 온톨로지 정의와 개념은 같은 ID로 덮어쓸 수 없다. 소스 변경은 새 버전 ID로 등록하고 관계 변경은 새 관계와 정정 이력을 사용한다.

오류 분기는 `engine.HasCode(err, engine.EvidenceMismatch)`처럼 확인한다.

## 현재 범위

실제 SQLite 파일을 사용하는 Go 코어, 로컬 CLI, 배포 가능한 스킬과 테스트를 제공한다. MCP, HTTP 서버, 독립 AI 실행기, 시각화, 올바름 판정은 포함하지 않는다. 대화의 관찰 추출은 기존 AI가 스킬에 따라 수행하고 CLI가 구조를 검증한다.

코어는 근거 구간과 연결의 구조를 검증한다. 사용자가 실제로 해당 말을 했는지에 관한 외부 도구의 신뢰성과 관찰 종류의 의미적 정확성을 자동 보증하지 않는다. 원문과 출처를 함께 반환한다.

정확한 타입과 계약은 [코어 설계서](docs/core-engine-design.md)와 [공개 Go 타입](engine/types.go)을 참고한다.
