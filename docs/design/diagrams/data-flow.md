# 数据流图

## usage 与降权

```mermaid
sequenceDiagram
  participant CPA as CPA Host
  participant A as CPA Adapter
  participant U as Usage Service
  participant D as Domain
  participant S as State Store
  participant Q as Demotion Queue
  participant W as Demotion Worker
  participant M as Management REST

  CPA->>A: usage.handle(auth_index, optional event_id, tokens, outcome)
  A->>A: validate + exact/weak dedupe
  A->>U: HandleUsage(normalized event)
  U->>D: apply real counters and outcome
  D-->>U: state change / demotion decision
  U->>S: atomic persist counters + dedupe
  alt threshold reached
    U->>Q: enqueue(auth_index, target priority)
    U-->>CPA: handler returns without PATCH
    Q->>W: dequeue unique intent
    W->>M: GET auth-files; resolve exact name
    W->>M: PATCH fields(name, target priority)
    W->>M: GET auth-files; verify result
    W->>S: persist demotion state + audit
  end
```

## 安全删除

```mermaid
sequenceDiagram
  participant UI
  participant API
  participant APP as Cleanup Service
  participant HOST as Management REST
  participant STATE as State Store

  UI->>API: preflight(auth_index, exact file_name)
  API->>APP: authenticated request
  APP->>HOST: GET auth-files; verify double key
  APP->>APP: evaluate protection rules
  APP-->>UI: risk summary + short-lived confirmation token
  UI->>API: confirm(same identity, token, confirmation text)
  API->>APP: authenticated confirmation
  APP->>HOST: re-list, then DELETE exact name
  APP->>HOST: re-list and verify absence
  HOST-->>APP: result
  APP->>STATE: audit + tombstone
  APP-->>UI: final per-account result
```
