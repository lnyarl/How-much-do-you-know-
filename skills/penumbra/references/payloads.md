# 입력 형식 예제

아래 값은 형식 설명용이다. 실제 사용자 데이터로 제출하지 않는다. 발언, 소스, 시각과 ID는 실제 관찰에서 작성한다.

## 온톨로지 등록

```json
{
  "producer_id": "assistant",
  "request_id": "ontology-request-001",
  "sources": [
    {"id":"source:database","namespace":"db","title":"사용자가 제공한 데이터베이스 자료"}
  ],
  "versions": [
    {"id":"source:database:v1","source_id":"source:database","content":"트랜잭션은 여러 작업을 묶는 개념이다. 격리 수준은 트랜잭션과 관련된 개념이다."}
  ],
  "concepts": [
    {"id":"db:transaction","namespace":"db","kind":"concept","label":"트랜잭션"},
    {"id":"db:isolation","namespace":"db","kind":"concept","label":"격리 수준"}
  ],
  "concept_sources": [
    {
      "id":"source-link-001",
      "concept_id":"db:transaction",
      "source_version_id":"source:database:v1",
      "quote":"트랜잭션은 여러 작업을 묶는 개념이다. 격리 수준은 트랜잭션과 관련된 개념이다."
    }
  ]
}
```

개념 타입은 topic, concept, mechanism, procedure, example이다. 소스를 등록하면 versions.content에는 해당 버전 원문을 보존한다. 전체 원문이 아닌 구간을 인용할 때는 span을 지정한다. 소스가 바뀌면 새 버전 ID를 쓴다.

소스 관계의 기본 타입은 is_a, part_of, concretizes, example_of, requires, related_to다. example_of의 출발 개념 타입은 example이어야 한다. 각 관계는 다음 형태다.

```json
{
  "id": "relation-001",
  "subject_id": "db:isolation",
  "type_id": "related_to",
  "object_id": "db:transaction",
  "source_version_id": "source:database:v1",
  "quote": "트랜잭션은 여러 작업을 묶는 개념이다. 격리 수준은 트랜잭션과 관련된 개념이다."
}
```

관계 조각은 등록 요청의 relations 배열에 넣는다. 부분 인용을 쓴다면 span의 end는 인용 마지막 바이트 다음 위치다. 인용 문자열과 바이트 구간이 정확히 일치해야 한다. 관계의 subject와 object는 먼저 등록돼 있어야 한다. 모호한 source 관계를 만들어 등록하지 않는다.

## 사용자 발언과 관찰

```json
{
  "producer_id": "assistant",
  "request_id": "feedback-request-001",
  "session_id": "session-001",
  "external_session_id": "conversation-001",
  "messages": [
    {
      "id":"message-001",
      "external_message_id":"turn-001",
      "role":"user",
      "content":"트랜잭션은 여러 쿼리를 하나로 묶는 거라고 알고 있어요.",
      "observed_at":"2026-11-10T05:00:00Z"
    }
  ],
  "observations": [
    {
      "id":"observation-001",
      "kind":"user_explanation",
      "message_id":"message-001",
      "quote":"트랜잭션은 여러 쿼리를 하나로 묶는 거라고 알고 있어요.",
      "assistance":"unknown",
      "concepts":[
        {"id":"binding-001","concept_id":"db:transaction","binding":"explicit"}
      ]
    }
  ]
}
```

quote가 메시지 전체이면 span을 생략할 수 있다. 부분 인용이면 start와 end를 UTF-8 바이트로 계산한다. 사용자 발언의 이름이 생략돼 있거나 대상이 모호하면 binding을 candidate로 둔다.

assistance는 none, hint, explanation, unknown이다. question이나 hint 메시지도 messages에 원문으로 넣고 관찰의 context_message_ids로 해당 ID를 지정한다.

user_confirmed 연결에는 confirmation_observation_id가 필요하다. 확인 관찰의 kind는 user_confirms_reference이고 같은 세션의 user 발언이어야 한다. 그 발언에 대상의 모호하지 않은 이름이나 namespaced ID가 있어야 한다.

새로 배웠다는 사실을 AI가 추론해 별도 상태로 만들지 않는다. 사용자가 다시 설명했다면 새 user_explanation을 기록한다. 사용자가 기억났다고 직접 말했으면 user_reports_recalled를 기록한다.
