# Firestore Storage Backend

A pluggable storage backend for the platform API server using Google Cloud Firestore.

## Usage

```go
import "github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/firestore"

factory := firestore.NewStorageFactory(firestore.StorageFactoryConfig{
    ProjectID: "my-project",
})
```

## Data Model

| Collection | Document ID | Purpose |
|---|---|---|
| `resources_{type}` | `{namespace}_{name}` | Resource documents (JSON data, labels, resource version) |
| `eventlog_{type}` | Zero-padded resource version | Event log for watch/subscribe replay |
| `counters` | `rv_{type}` | Monotonic resource version counter per type |

## Limitations

### Resource Version Counter Bottleneck

Firestore has no atomic increment-and-return operation. Resource versioning uses a Firestore transaction (read-increment-write) on a single counter document per resource type. Firestore enforces a **~1 sustained write/second soft limit per document**, which makes the counter a bottleneck under load.

- At **< 10 writes/sec** per resource type: works reliably with occasional transaction retries.
- At **10–50 writes/sec**: transaction contention causes retry storms and increased latency.
- At **> 50 writes/sec**: not viable without architectural changes.

Distributed counter shards could increase throughput but would break the total ordering required by the Kubernetes watch protocol.

### Composite Index Requirements

Firestore requires composite indexes for queries combining multiple fields. Since Kubernetes labels are arbitrary key-value pairs, server-side label filtering is impractical for arbitrary selectors. Label selectors, shard selectors, and field filters are evaluated **client-side** after fetching documents.

### Snapshot Listener Reconnection

Firestore snapshot listeners (used for event broadcasting) can return `iterator.Done` unexpectedly due to network issues. The broadcaster includes reconnection logic with a 1-second backoff, but there may be brief gaps in real-time event delivery during reconnections.

### Document ID Constraints

Firestore document IDs cannot contain `/`. Resource names and namespaces are joined with `_` instead of `/` or `\x00`. If resource names or namespaces contain underscores, there is a theoretical (but unlikely in practice) collision risk.

## Comparison with Other Backends

| | In-Memory | PostgreSQL | Bigtable | Firestore |
|---|---|---|---|---|
| **Resource versioning** | `atomic.Int64` | `pg_current_xact_id()` | `ReadModifyWriteRow` (atomic increment + return) | Transaction (read-increment-write, ~1 write/sec/doc limit) |
| **Event broadcasting** | In-process ring buffer | `LISTEN/NOTIFY` | Change streams (low-level gRPC) | Snapshot listeners (high-level API) |
| **Duplicate detection** | Map key check | `INSERT ... ON CONFLICT` | `CheckAndMutateRow` (conditional mutation) | `DocumentRef.Create()` (native `AlreadyExists`) |
| **List pagination** | Client-side slice | SQL `LIMIT`/`OFFSET` with tuple comparison | Manual key range (`NewRange`) | Native `OrderBy` + `StartAfter` + `Limit` |
| **Label/field filtering** | Client-side | Server-side (JSONB operators) | Client-side | Client-side (composite index constraints) |
| **Consistency** | Immediate | Serializable transactions | Row-level strong consistency | Strongly consistent |
| **Persistence** | None | Durable | Durable | Durable |
| **Scale ceiling** | Single process | Single instance (with replicas) | Petabyte-scale, linear scaling | 10K writes/sec per database |
| **Operational complexity** | None | Manage DB instances | Provision clusters/nodes | Fully serverless |
| **Cost at small scale** | Free | DB instance cost | Minimum node cost | Pay-per-operation |
| **Emulator** | N/A | Docker container | `go install .../bigtable/cmd/emulator` | `gcloud emulators firestore start` (requires Java) |

### When to Use Each

- **In-Memory**: Development, testing, single-instance deployments where persistence is unnecessary.
- **PostgreSQL**: Production deployments needing server-side query capabilities (label filtering in SQL), strong transactional guarantees, and moderate scale.
- **Bigtable**: High-throughput production workloads (> 50 writes/sec per resource type). Best resource versioning performance due to atomic increment-and-return.
- **Firestore**: Small-to-moderate deployments (< 10 writes/sec per resource type) where serverless operations, zero capacity planning, and pay-per-use pricing are priorities.

## Running Tests

```bash
# Start the Firestore emulator and run tests
make test-firestore

# Or manually
gcloud emulators firestore start --host-port=localhost:8090 &
FIRESTORE_EMULATOR_HOST=localhost:8090 go test -v -count=1 ./pkg/apiserver/storage/firestore/...
```
