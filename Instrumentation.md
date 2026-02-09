# Instrumentation Strategy

## 1. Overview

This document defines the instrumentation strategy for the observability platform. We use a **hybrid layered approach** — combining auto-instrumentation with targeted manual instrumentation to balance coverage, depth, and developer effort.

### Guiding Principle

> Instrument everything automatically. Instrument important things deeply. Instrument critical things manually.

---

## 2. Instrumentation Layers

```mermaid
graph TB
    subgraph Layer3["Layer 3: Manual SDK Spans"]
        L3["Custom spans for critical business logic<br/>Custom attributes (user.id, order.total)<br/>Fine-grained error handling<br/><br/>Effort: High · Coverage: Targeted · Depth: Maximum"]
    end

    subgraph Layer2["Layer 2: Instrumentation Libraries"]
        L2["Framework-aware auto-spans<br/>otelhttp, otelsql, otelgrpc, otelredis<br/>1-2 lines of code per integration<br/><br/>Effort: Low · Coverage: Per-service · Depth: Moderate"]
    end

    subgraph Layer1["Layer 1: Beyla eBPF Auto-Instrumentation"]
        L1["Zero code changes<br/>HTTP/gRPC entry-exit spans + RED metrics<br/>Deployed as DaemonSet<br/><br/>Effort: None · Coverage: All services · Depth: Surface"]
    end

    Layer3 --- Layer2 --- Layer1

    style Layer1 fill:#1b4332,color:#fff
    style Layer2 fill:#2d6a4f,color:#fff
    style Layer3 fill:#40916c,color:#fff
```

### Layer Summary

| Layer | What | How | Code Changes | Coverage | Depth |
|-------|------|-----|-------------|----------|-------|
| **Layer 1** | Beyla eBPF | DaemonSet on every node | None | All services | Entry/exit spans |
| **Layer 2** | Instrumentation libraries | Wrap handlers/clients | 1-2 lines per integration | Per-service opt-in | Framework-level spans |
| **Layer 3** | Manual SDK spans | `tracer.Start(ctx, "name")` | Per-operation | Targeted | Full business context |

---

## 3. Layer 1: Beyla eBPF Auto-Instrumentation

### 3.1 How It Works

Beyla attaches eBPF probes to the Linux kernel's networking stack. It observes HTTP/gRPC requests entering and leaving processes without modifying the application.

```mermaid
sequenceDiagram
    participant Client
    participant Kernel as Linux Kernel
    participant App as Application Process
    participant Beyla as Beyla (eBPF Agent)
    participant Alloy as Alloy Gateway
    participant Tempo

    Client->>Kernel: TCP SYN → App:8080
    Kernel-->>Beyla: kprobe: socket accept

    Client->>App: GET /api/users
    Kernel-->>Beyla: uprobe: HTTP request detected<br/>method=GET, path=/api/users

    Note over App: Application processes<br/>the request (opaque to Beyla)

    App->>Client: 200 OK (145ms)
    Kernel-->>Beyla: uprobe: HTTP response detected<br/>status=200, duration=145ms

    Beyla->>Beyla: Create span:<br/>name="GET /api/users"<br/>duration=145ms<br/>status=200<br/>k8s.namespace="team-alpha"

    Beyla->>Beyla: Update RED metrics:<br/>http_server_request_duration_seconds

    Beyla->>Alloy: OTLP push (span + metrics)
    Alloy->>Tempo: Forward with tenant header
```

### 3.2 What Beyla Captures

```mermaid
flowchart LR
    subgraph Captured["What Beyla Sees"]
        HTTP["HTTP Calls<br/>method, path, status, duration"]
        gRPC["gRPC Calls<br/>service, method, status, duration"]
        SQL["SQL Queries<br/>operation, table, duration"]
        Redis["Redis Commands<br/>command, duration"]
        Kafka["Kafka Messages<br/>topic, operation, duration"]
    end

    subgraph NotCaptured["What Beyla Cannot See"]
        Business["Business Logic<br/>pricing calculations, validation"]
        Custom["Custom Attributes<br/>user.id, order.total, tenant"]
        Internal["Internal Functions<br/>helper methods, goroutines"]
        Conditional["Conditional Flows<br/>if/else branches, retries"]
    end

    style Captured fill:#2d6a4f,color:#fff
    style NotCaptured fill:#9b2226,color:#fff
```

### 3.3 What a Beyla-Only Trace Looks Like

```
POST /api/checkout → 200 (450ms)
```

Single span. You know the request happened and how long it took. You don't know why it took 450ms.

### 3.4 When Layer 1 Is Sufficient

- Services with simple request/response patterns
- Third-party or legacy services you can't modify
- Early-stage services where you just need RED metrics
- Broad coverage during initial observability rollout

---

## 4. Layer 2: Instrumentation Libraries

### 4.1 How It Works

OpenTelemetry provides instrumentation libraries that wrap common frameworks and clients. You replace your HTTP handler or DB client with an instrumented version. The library automatically creates spans for every operation.

```mermaid
sequenceDiagram
    participant Client
    participant OtelHTTP as otelhttp Handler
    participant App as Application Logic
    participant OtelSQL as otelsql Client
    participant DB as Database
    participant OtelGRPC as otelgrpc Client
    participant ExtSvc as External Service

    Client->>OtelHTTP: POST /api/checkout
    activate OtelHTTP
    Note over OtelHTTP: Auto-create span:<br/>"POST /api/checkout"

    OtelHTTP->>App: Handle(ctx, req)
    activate App

    App->>OtelSQL: Query(ctx, "SELECT * FROM cart")
    activate OtelSQL
    Note over OtelSQL: Auto-create child span:<br/>"SELECT cart"
    OtelSQL->>DB: Execute query
    DB-->>OtelSQL: Results (12ms)
    deactivate OtelSQL

    App->>OtelGRPC: Call(ctx, PaymentService.Charge)
    activate OtelGRPC
    Note over OtelGRPC: Auto-create child span:<br/>"grpc PaymentService/Charge"<br/>+ inject trace context header
    OtelGRPC->>ExtSvc: gRPC call with traceparent header
    ExtSvc-->>OtelGRPC: Response (400ms)
    deactivate OtelGRPC

    App->>OtelSQL: Exec(ctx, "INSERT INTO orders")
    activate OtelSQL
    Note over OtelSQL: Auto-create child span:<br/>"INSERT orders"
    OtelSQL->>DB: Execute query
    DB-->>OtelSQL: OK (30ms)
    deactivate OtelSQL

    App-->>OtelHTTP: Response
    deactivate App
    OtelHTTP-->>Client: 200 OK (450ms)
    deactivate OtelHTTP
```

### 4.2 Code Changes Required

**Go — before (no instrumentation):**
```go
mux := http.NewServeMux()
mux.HandleFunc("/checkout", checkoutHandler)
http.ListenAndServe(":8080", mux)
```

**Go — after (Layer 2 instrumentation):**
```go
import "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

mux := http.NewServeMux()
mux.HandleFunc("/checkout", checkoutHandler)
http.ListenAndServe(":8080", otelhttp.NewHandler(mux, "checkout-svc"))
//                           ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
//                           One line wraps all routes with tracing
```

**Database client:**
```go
import "github.com/XSAM/otelsql"

db, _ := otelsql.Open("postgres", dsn)
// Every query now auto-generates a span
```

### 4.3 Available Instrumentation Libraries

| Integration | Library | Auto-generates |
|------------|---------|---------------|
| **net/http** | `otelhttp` | Span per HTTP request + response attributes |
| **gRPC** | `otelgrpc` | Span per RPC call + status codes |
| **database/sql** | `otelsql` | Span per query with statement text |
| **Redis** | `otelredis` | Span per Redis command |
| **Kafka** | `otelkafka` | Span per produce/consume |
| **AWS SDK** | `otelaws` | Span per AWS API call |
| **Gin** | `otelgin` | Span per Gin route |
| **Echo** | `otelecho` | Span per Echo route |

### 4.4 What a Layer 2 Trace Looks Like

```
POST /api/checkout → 200 (450ms)
├── DB: SELECT * FROM cart (12ms)
├── gRPC: PaymentService/Charge (400ms)
│     └── [payment-svc] POST /charge → 200 (395ms)     ← cross-service
└── DB: INSERT INTO orders (30ms)
```

You can now see that the payment service call is the bottleneck. No custom code was written — just wrapping the HTTP handler and DB client.

### 4.5 When to Add Layer 2

- Services identified as important through Layer 1 RED metrics
- When you need to know *which dependency* is causing latency
- Services that make multiple downstream calls (APIs, databases, caches)
- Cross-service trace correlation is needed

---

## 5. Layer 3: Manual SDK Spans

### 5.1 How It Works

Developers create custom spans around specific code blocks to capture business-level context that no auto-instrumentation can see.

```mermaid
sequenceDiagram
    participant Client
    participant Handler as HTTP Handler<br/>(Layer 2: otelhttp)
    participant Validate as validateCart()<br/>(Layer 3: manual span)
    participant Price as calculatePricing()<br/>(Layer 3: manual span)
    participant DB as DB Client<br/>(Layer 2: otelsql)
    participant Payment as chargePayment()<br/>(Layer 3: manual span)
    participant Fraud as fraudCheck()<br/>(Layer 3: manual span)
    participant ExtAPI as Payment Gateway

    Client->>Handler: POST /checkout
    activate Handler
    Note over Handler: Auto-span: POST /checkout

    Handler->>Validate: validateCart(ctx, cartID)
    activate Validate
    Note over Validate: Manual span: "validateCart"<br/>cart.items=3, cart.total=149.99
    Validate-->>Handler: valid
    deactivate Validate

    Handler->>Price: calculatePricing(ctx, cart)
    activate Price
    Note over Price: Manual span: "calculatePricing"<br/>subtotal=149.99<br/>discount.code="SAVE10"<br/>discount.percent=10%<br/>tax=13.50<br/>total=148.49
    Price-->>Handler: pricing result
    deactivate Price

    Handler->>DB: INSERT INTO orders
    Note over DB: Auto-span: INSERT orders (Layer 2)

    Handler->>Payment: chargePayment(ctx, order)
    activate Payment
    Note over Payment: Manual span: "chargePayment"<br/>order.id=ORD-98765<br/>amount=148.49<br/>currency=USD

    Payment->>Fraud: fraudCheck(ctx, user, amount)
    activate Fraud
    Note over Fraud: Manual span: "fraudCheck"<br/>risk.score=0.82<br/>risk.reason="new device"<br/>user.id=U-12345
    Fraud-->>Payment: pass (350ms)
    deactivate Fraud

    Payment->>ExtAPI: Charge $148.49
    ExtAPI-->>Payment: confirmed
    deactivate Payment

    Handler-->>Client: 200 OK
    deactivate Handler
```

### 5.2 Code Example

```go
func checkoutHandler(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    tracer := otel.Tracer("checkout-svc")

    // Manual span for business logic
    ctx, validateSpan := tracer.Start(ctx, "validateCart")
    cart, err := validateCart(ctx, cartID)
    validateSpan.SetAttributes(
        attribute.Int("cart.items", len(cart.Items)),
        attribute.Float64("cart.total", cart.Total),
    )
    if err != nil {
        validateSpan.RecordError(err)
        validateSpan.SetStatus(codes.Error, "cart validation failed")
    }
    validateSpan.End()

    // Manual span for pricing — captures business context
    ctx, priceSpan := tracer.Start(ctx, "calculatePricing")
    pricing := calculatePricing(ctx, cart)
    priceSpan.SetAttributes(
        attribute.Float64("subtotal", pricing.Subtotal),
        attribute.String("discount.code", pricing.DiscountCode),
        attribute.Float64("discount.percent", pricing.DiscountPercent),
        attribute.Float64("tax", pricing.Tax),
        attribute.Float64("total", pricing.Total),
    )
    priceSpan.End()

    // DB call — auto-instrumented by otelsql (Layer 2)
    db.ExecContext(ctx, "INSERT INTO orders ...")

    // Manual span for payment — captures order context
    ctx, paySpan := tracer.Start(ctx, "chargePayment")
    paySpan.SetAttributes(
        attribute.String("order.id", order.ID),
        attribute.Float64("amount", pricing.Total),
    )
    result, err := chargePayment(ctx, order)
    paySpan.End()
}
```

### 5.3 What a Full Hybrid Trace Looks Like

```
POST /api/checkout → 200 (450ms)                           ← Layer 1 (Beyla) or Layer 2 (otelhttp)
├── validateCart (5ms)                                      ← Layer 3 (manual)
│     cart.items=3, cart.total=149.99
├── calculatePricing (8ms)                                  ← Layer 3 (manual)
│     subtotal=149.99, discount=10%, tax=13.50
├── DB: SELECT * FROM cart (12ms)                           ← Layer 2 (otelsql)
├── chargePayment (400ms)                                   ← Layer 3 (manual)
│     order.id=ORD-98765, amount=148.49
│     ├── fraudCheck (350ms)                                ← Layer 3 (manual)
│     │     risk.score=0.82, reason="new device"
│     └── HTTP: POST stripe.com/v1/charges (45ms)           ← Layer 2 (otelhttp)
└── DB: INSERT INTO orders (30ms)                           ← Layer 2 (otelsql)
```

### 5.4 When to Add Layer 3

- Payment processing, checkout flows, order fulfillment
- Authentication and authorization decisions
- Complex business calculations (pricing, scoring, matching)
- Debugging a specific production issue
- Regulatory/audit requirements for traceability

---

## 6. Comparison Matrix

### 6.1 Capabilities

```mermaid
quadrantChart
    title Instrumentation Trade-offs
    x-axis Low Effort --> High Effort
    y-axis Shallow Depth --> Deep Depth
    quadrant-1 Manual SDK
    quadrant-2 Ideal (not real)
    quadrant-3 Beyla eBPF
    quadrant-4 Instrumentation Libraries
```

### 6.2 Detailed Comparison

| Aspect | Layer 1: Beyla | Layer 2: Libraries | Layer 3: Manual |
|--------|---------------|-------------------|----------------|
| **Code changes** | None | 1-2 lines per integration | Per-operation |
| **Deploy method** | DaemonSet | Per-service code change | Per-service code change |
| **Span depth** | Entry/exit only | Per-framework operation | Full custom |
| **Custom attributes** | No | Limited (framework attrs) | Full control |
| **Cross-service traces** | Yes (via headers) | Yes (context propagation) | Yes (context propagation) |
| **Business context** | No | No | Yes |
| **Error detail** | HTTP status code | Framework errors | Custom error messages |
| **Performance overhead** | Very low (~1%) | Low (~2-3%) | Low-moderate (~3-5%) |
| **Maintenance** | None | Update library versions | Update with code changes |
| **Language support** | Any | Language-specific | Language-specific |
| **Kernel requirement** | Linux 5.8+, privileged | None | None |
| **Rollout speed** | Minutes (one DaemonSet) | Hours (per service) | Days/weeks (per feature) |

---

## 7. Decision Framework

### 7.1 When to Use What

```mermaid
flowchart TD
    Start["New service added<br/>to the cluster"] --> L1["Layer 1: Beyla<br/>Auto-covers it immediately"]

    L1 --> Q1{"Need to know<br/>which dependency<br/>is slow?"}

    Q1 -->|No| Done1["Layer 1 is sufficient"]
    Q1 -->|Yes| L2["Add Layer 2:<br/>Wrap HTTP handler + DB client<br/>(1-2 lines of code)"]

    L2 --> Q2{"Need business context?<br/>(user.id, order.total,<br/>risk.score)"}

    Q2 -->|No| Done2["Layer 1 + 2 is sufficient"]
    Q2 -->|Yes| Q3{"Is this a critical<br/>business flow?"}

    Q3 -->|No| Done2
    Q3 -->|Yes| L3["Add Layer 3:<br/>Manual spans on<br/>critical operations"]

    L3 --> Done3["Full hybrid instrumentation"]

    style L1 fill:#1b4332,color:#fff
    style L2 fill:#2d6a4f,color:#fff
    style L3 fill:#40916c,color:#fff
    style Done1 fill:#333,color:#fff
    style Done2 fill:#333,color:#fff
    style Done3 fill:#333,color:#fff
```

### 7.2 Typical Service Distribution

In a production environment with ~50 services:

```mermaid
pie title Instrumentation Layer Distribution
    "Layer 1 only (Beyla)" : 70
    "Layer 1 + 2 (Libraries)" : 20
    "Layer 1 + 2 + 3 (Full hybrid)" : 10
```

- **~70% of services**: Layer 1 only — background services, simple CRUD, internal tools
- **~20% of services**: Layer 1 + 2 — API gateways, services with multiple dependencies
- **~10% of services**: Full hybrid — checkout, payments, auth, core business logic

---

## 8. Trace Context Propagation

### 8.1 How Layers Merge Into One Trace

All three layers use the **W3C Trace Context** standard (`traceparent` header). Spans from different layers automatically parent correctly because they share the same trace context.

```mermaid
sequenceDiagram
    participant Beyla as Beyla (Layer 1)
    participant OtelHTTP as otelhttp (Layer 2)
    participant Manual as Manual Span (Layer 3)
    participant OtelSQL as otelsql (Layer 2)

    Note over Beyla: Beyla sees HTTP request enter<br/>Creates root span (or detects<br/>existing traceparent header)

    Note over OtelHTTP: otelhttp creates server span<br/>Parents under Beyla or incoming context

    Note over Manual: tracer.Start(ctx, "businessLogic")<br/>Parents under otelhttp span<br/>via Go context propagation

    Note over OtelSQL: db.QueryContext(ctx, ...)<br/>Parents under manual span<br/>via Go context propagation

    Note over Beyla,OtelSQL: All 4 spans share the same trace_id<br/>Parent-child chain is automatic
```

### 8.2 Context Flow

```
trace_id: abc123 (shared across all spans)

Beyla span         ─────────────────────────────────── (450ms)
 └─ otelhttp span  ────────────────────────────────── (448ms)
     ├─ manual span  ──── (5ms)   validateCart
     ├─ otelsql span  ─── (12ms)  SELECT cart
     ├─ manual span  ────────────────────────── (400ms) chargePayment
     │   ├─ manual span  ──────────────────── (350ms)   fraudCheck
     │   └─ otelhttp span  ──── (45ms)                  POST stripe
     └─ otelsql span  ─── (30ms)  INSERT orders
```

When Beyla and OTel SDK both create spans for the same request, the deduplication happens naturally: Beyla's span becomes an outer wrapper, and the SDK spans nest inside via context propagation. If the SDK already provides a root span, Beyla detects the existing `traceparent` and avoids creating a duplicate.

---

## 9. Implementation Rollout Plan

### Phase 1: Baseline (Week 1)

Deploy Beyla as a DaemonSet. Every service gets auto-instrumented immediately.

```mermaid
gantt
    title Instrumentation Rollout
    dateFormat  YYYY-MM-DD
    section Phase 1
        Deploy Beyla DaemonSet           :p1a, 2025-01-01, 2d
        Verify traces in Tempo           :p1b, after p1a, 1d
        Verify RED metrics in Mimir      :p1c, after p1a, 1d
        Create RED dashboards            :p1d, after p1b, 2d

    section Phase 2
        Identify top 10 critical services  :p2a, after p1d, 2d
        Add otelhttp to API gateway        :p2b, after p2a, 1d
        Add otelsql to data services       :p2c, after p2a, 2d
        Add otelgrpc to service mesh       :p2d, after p2a, 2d

    section Phase 3
        Add manual spans to checkout flow  :p3a, after p2c, 3d
        Add manual spans to auth flow      :p3b, after p2c, 2d
        Add manual spans to payment flow   :p3c, after p3a, 3d
```

**Outcome**: RED metrics and basic traces for all services.

### Phase 2: Targeted Depth (Week 2-3)

Add instrumentation libraries to the top 10 most critical services.

**Outcome**: Dependency-level visibility for important services.

### Phase 3: Business Context (Week 3-4)

Add manual spans to the 3-5 most critical business flows.

**Outcome**: Full business observability for critical paths.

---

## 10. Guidelines for Developers

### Do

- Let Beyla handle baseline coverage — don't manually instrument what Beyla already captures
- Use instrumentation libraries before writing manual spans
- Pass `context.Context` through all function calls (required for span parenting)
- Add attributes that help with debugging: IDs, counts, amounts, status reasons
- Use `span.RecordError(err)` to attach errors to spans
- Keep span names stable (use `"processOrder"` not `"processOrder-12345"`)

### Don't

- Don't create a span for every function — instrument boundaries, not internals
- Don't put high-cardinality values in span **names** (use attributes instead)
- Don't log and trace the same thing redundantly — link them via `trace_id`
- Don't instrument tight loops (millions of spans = performance degradation)
- Don't skip `span.End()` — use `defer span.End()` immediately after creation

### Span Naming Conventions

| Pattern | Example | Good For |
|---------|---------|----------|
| `verb + noun` | `validateCart`, `chargePayment` | Business operations |
| `HTTP method + route` | `GET /api/users` | HTTP handlers (auto-generated) |
| `DB operation + table` | `SELECT users` | Database queries (auto-generated) |
| `service/method` | `PaymentService/Charge` | gRPC calls (auto-generated) |

### Attribute Naming Conventions

Follow [OpenTelemetry Semantic Conventions](https://opentelemetry.io/docs/specs/semconv/):

| Attribute | Example | When |
|-----------|---------|------|
| `http.method` | `GET` | Auto (Layer 1/2) |
| `http.status_code` | `200` | Auto (Layer 1/2) |
| `db.statement` | `SELECT * FROM users` | Auto (Layer 2) |
| `user.id` | `U-12345` | Manual (Layer 3) |
| `order.id` | `ORD-98765` | Manual (Layer 3) |
| `payment.amount` | `148.49` | Manual (Layer 3) |
| `error.reason` | `insufficient_funds` | Manual (Layer 3) |
