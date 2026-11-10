# CLI 계약

아래 penu는 준비 단계에서 찾은 실행 파일이다. 저장소에서는 ./bin/penu로 바꿔 실행한다.

```sh
penu version
penu --db /absolute/path/knowledge.db init
penu --db /absolute/path/knowledge.db meta
```

--db는 전역 플래그라서 명령 앞에 둔다. 생략하면 PENU_DB_PATH, 그다음 OS 사용자 설정 디렉터리 아래 penumbra/knowledge.db를 사용한다. init 출력의 path는 실제 절대 경로다.

## 조회

```sh
penu concepts search --namespace db --limit 20 '트랜잭션'
penu ontology explore --concept db:transaction --direction both --depth 2 --limit 100
penu concepts sources --concept db:transaction
penu sources get --version source:database:v1
penu context get --concept db:transaction --concept db:isolation --max-items 30 --max-bytes 65536
penu evidence get observation-001 observation-002
```

이름 검색의 플래그는 검색어 앞에 둔다. 같은 이름의 결과가 여러 개면 ID와 namespace를 확인하고 자동 병합하지 않는다.

context get은 요청 ID의 직접 관찰만 반환한다. ontology explore의 관계는 사용자의 이해 근거가 아니다. 후보 연결은 기본 제외다. 필요할 때 --include-candidates로 조회하되 후보임을 유지한다.

sources get은 원문을 반환한다. concepts sources의 source_version_id를 사용한다. 소스 위치를 찾을 수 없으면 출처를 만들어내지 않는다.

next_cursor가 있으면 같은 조회 조건에 --cursor '값'을 추가한다. 커서 조회 중 예산이나 개념 집합을 바꾸지 않는다.

## 쓰기

```sh
penu concepts register --file ontology.json
penu feedback submit --file feedback.json
penu amend submit --file amendment.json
```

--file -는 stdin JSON을 읽는다. request_id는 producer_id 안에서 작업 종류를 포함해 유일해야 한다. 재전송 시 같은 ID와 같은 입력을 유지한다. 관찰을 바꾸려면 새 ID와 정정 이력을 사용한다.

외부 메시지 ID는 같은 세션 안에서 유일해야 한다. 메시지는 user, assistant, system 역할을 구분하며 사용자 지식의 근거는 user 원문만 사용할 수 있다.

성공 출력은 stdout의 단일 JSON 값이다. 오류는 stderr의 다음 형식이다.

```json
{"error":{"code":"MISSING_EVIDENCE","message":"..."}}
```

종료 코드는 성공 0, CLI 입력 오류 2, 엔진 또는 I/O 오류 1이다. STORE_NOT_FOUND이면 등록 작업에 한해 init으로 초기화할 수 있다. 주제별로 임의의 새 DB를 만들지 않는다.

## 정정

```json
{
  "producer_id": "assistant",
  "request_id": "amendment-request-001",
  "amendment": {
    "id": "amendment-001",
    "target_type": "observation",
    "target_id": "observation-001",
    "action": "retract",
    "mode": "extraction_error",
    "reason": "발언의 대상 개념을 잘못 연결함"
  }
}
```

target_type은 observation, binding, assertion, relation이다. replace에는 같은 타입의 활성 replacement_id가 필요하다. binding 교체는 같은 관찰에 속해야 한다.

user_statement 모드는 실제 정정에 해당하는 user_confirms_reference 관찰을 evidence_observation_id로 연결한다. 사용자 발언을 임의로 수정하는 데 사용하지 않는다.
