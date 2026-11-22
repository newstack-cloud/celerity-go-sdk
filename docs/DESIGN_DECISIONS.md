# Design Decisions

Why the SDK is shaped the way it is, decision by decision. Where Node and Python are named, the
decision is shared with, or departs from, the other Celerity SDKs.

**A cache is one implementation for every platform.** Every managed cache
speaks Redis: ElastiCache, Memorystore, Azure Cache, and the Valkey a
development session runs. So unlike a queue or a bucket there is no per-platform
cache, and `resources/redis` holds the whole of it. What differs by platform is
only how a password is obtained, which is the `redis.Credentials` seam: AWS
supplies one that reads Secrets Manager and signs an IAM connection token, and
contributes nothing else. The Node and Python SDKs draw the same line, with a
`providers/redis` directory and the cloud only under `redis/iam`.

**The application names no platform; the build does.** Go links what is
imported and cannot load code afterwards, so the adapter package must be named
before the build. `celerity-go generate` writes `celerity_platform_gen.go` from
the blueprint's deploy target, and the Celerity CLI runs it as part of building.
Only the target's modules are linked, plus, with `--local`, what a development
session reads. The developer writes no blank imports;
that remains available for building outside the CLI.

**Adapters and resource providers self-register.** An `init` calls
`serverless.Register` / `resources.RegisterProvider`, which is what lets the
generated file be blank imports rather than generated wiring, and what `Detect`
selects among when more than one is linked. Core contains no provider's
environment variables. `WithAdapter` and `WithResourceProvider` remain for tests
supplying fakes.

**No DI container, no decorators.** Registration is an explicit function call
and dependencies arrive through the closure or the receiver, never through the
handler's context. A `context.Context` carries what is true of one dispatch,
the event, the trace context and the authorisation decision; a dependency that
exists before any event does and is the same object for all of them is passed
as an argument, so that the graph the CLI recovers from the source is the graph
the program actually has. The two capabilities that are per-dispatch and cannot
be closed over, the WebSocket sender and the event-scoped logger, are the
deliberate exceptions and say so where they are defined. Registration is a
package-level generic function rather than a method on `*App`. Until Go 1.27 that
was forced, since no method could declare type parameters; now it is a choice,
because a generic method on a concrete type would require raising the module's
language version and `*App` is concrete. The same is not true of the reads that
name an item type, such as `datastore.Items`: an interface method still cannot be
generic, so those have to stay functions.

**Handler tags must match the runtime byte for byte.** Formats live in
`celerity/tags.go` and mirror the runtime's `event_queue.rs`. A mismatch fails
the startup handshake rather than misrouting in production.

**Credit is granted on every result, including from a panic.** A result that
grants nothing shrinks the credit window permanently and stalls the stream with
nothing reported, so the grant is emitted by the same `defer` that recovers.

**The IPC frame loop is written against `Transport`, not gRPC types.** The
protocol's behaviour is testable without a transport, which is the separation
the runtime draws on its own side. `internal/ipc/convert.go` is the only file
that knows about generated types.

**The datastore's two correctness features are sort conditions and conditional
writes.** Without the first, a query reads a whole partition and a sort key is
decoration; without the second there is no compare-and-set, so concurrent
handlers silently lose updates. `SortCondition` carries no attribute name, since
the sort key is the store's the way `Key` is; `Condition` does, since those are
the item's own attributes. Conditions go through DynamoDB's own `expression`
builder rather than string building, which is what gets placeholder allocation
and nested and-or precedence right. `ConditionalCheckFailedException` is wrapped
as `resources.ErrConditionFailed` so a retry does not match on an AWS code.
`NotExists` is in the portable set although Node and Python leave it to their
provider classes: it is the most portable write condition of the lot, a native
precondition on all three stores, while `Eq` is in their portable set and needs
a transaction on two of three. The operation surface now matches Node and
Python: get, put, delete, query, scan, batch get, batch write, update and an
atomic write, with filters, projection, index selection and sort direction on a
query. Consistent read is in none of the three.

**A multiple delete reports the same three lists a batch send does.** S3 takes a
`DeleteObjects` request partially and caps it at a thousand keys, so
`DeleteMany` chunks and answers with `Deleted`, `Failed` and `Unsent`: a caller
told only that a delete of 2500 keys failed cannot tell that the first thousand
are gone. `ObjectRef` carries an optional version, so one call can remove a
mixture of current versions and named ones.

**A copy destination is a handle, not a bucket's name.** Node and Python both
took the deployed name of the other bucket, which a handler would have to have
learnt from somewhere; all three now take a handle and let the provider resolve
it. `bucket.Destination` carries a `Store`, taken the way the source was, so a
handler still names only blueprint resources. The two have to be the same
provider's, since the store does the copying, and a destination the provider
cannot resolve says so rather than failing as a type assertion.

**A listing returns one page and an opaque cursor.** Node and Python both
base64 the store's own position, which is what lets a query on a secondary index
resume: DynamoDB wants the index's key and the table's together, and a typed key
can only carry one pair. Go's `Query.StartFrom Key` could not express it, so it
is `Cursor string`, and `BucketStore.List` matches. Reading a listing through is
`resources.Items[T]` / `resources.Objects`, returning `iter.Seq2`, package-level
generic functions for the same reason registration is one.

**A batch accounts for every entry in one of three lists.** A queue send or a
topic publish of several messages is taken partially: the provider accepts the
entries it will and refuses the rest. So `SendBatch` and `PublishBatch` take
`[]BatchEntry` and answer with a `BatchResult` of `Successful`, `Failed` and
`Unsent`. A refusal is not an error, it is `Failed`. A request that did not
complete is `Unsent`, along with every entry the batch never got to, since a
batch over the provider's ceiling of ten is more than one request and a caller
told only that the batch failed would send the chunks that went a second time.
Every entry handed over lands in exactly one list, so a caller reads what to
retry rather than diffing the result against its input, and the three map to
three actions: done, fix, retry. The error return stays alongside, so a caller
checking only `err != nil` still sees that something went wrong. All three SDKs
share this shape: Node and Python returned `{successful, failed}` and threw from
inside the chunking loop, discarding the successes, which was the same
duplicate-send hazard.

An entry carries its own options, because a batch commonly spans message groups.
Each is addressed on the wire by its position rather than by the `ID` the caller
gave it: SQS and SNS hold the ids of one request to a narrow alphabet and require
them to be unique, neither of which a caller naming their own entries can be held
to, so `ID` stays purely a correlation handle and may be any string, repeated.
Node and Python do the same. The entry and result types are duplicated per
package rather than shared, for the same reason `SendOptions` is: the two look
alike and are not the same thing. A publish takes a `Subject`, which a send has
no equivalent of, and a send takes a `Delay`, which a publish does not. `Delay`
was on both for a while and no provider could honour the topic's: SNS and
Pub/Sub deliver on publish, and holding a message back is the subscribing
queue's to do, so both implementations existed only to refuse it. Removed, which
makes asking for one a compile error rather than a failure on the first message,
and leaves the Go contract matching Node's and Python's, neither of which ever
had it. Azure Service Bus can schedule a publish, and if that is ever wanted it
is an absolute time in the Azure provider rather than a duration here.

**A read returns the object's metadata with its content.** S3 answers a
`GetObject` with the headers either way, so `Get` returns a `*bucket.Object` of
both and a handler serving a download pays nothing to learn what it is serving.
A listing cannot fill `ContentType` or `Metadata`, because a store answers a
listing with keys and sizes rather than each object's headers, and a request per
object to fill them in is not something a listing should do behind the caller's
back. A `Range` is a start and a length rather than two positions, so that the
zero value is the whole object: a last byte of zero would otherwise be
indistinguishable from asking for the first byte alone.

**A ranged read reports two sizes, because it has two.** `Info.Size` is the
whole object's however the object was asked for, and `Object.ContentLength` is
how many bytes the body yields. Keeping one field for both would make size mean
one thing on a whole read and another on a ranged one, and a handler answering
an HTTP range request needs the pair: the total for `Content-Range`, the length
for `Content-Length`. Neither is the caller's to derive, because the range asked
for is not the range returned twice over: a range with no length reads to the
end, and a store satisfies a range overrunning the object with the bytes that
are left rather than refusing it. Node and Python each reported only one of the
two, and the one they reported was the derivable one: both took
`Content-Length`, so the whole object's size was unrecoverable from a ranged
get and a range-proxying handler could not build a `Content-Range` without a
second request. All three now parse the total out of `Content-Range` for the
size and carry the part alongside; an answer with no usable total, such as
`bytes 0-4/*`, falls back to the part.

**Only the portable isolation levels are named.** `sqldb` has its own
`IsolationLevel` rather than `database/sql`'s, holding default, read committed,
repeatable read and serializable. Read uncommitted is left out although both
engines accept it: Postgres runs read committed anyway, so a handler asking for
it would get one thing on one engine and another on the next. `ErrTxDone` is
re-exported so a deferred `Rollback` after a `Commit`, which is how a
transaction is reliably finished, can be told from a failure.

**A development session serves a queue and a topic and delegates the rest.**
Most of what a session runs speaks the protocol the real service speaks: MinIO
answers S3 requests and DynamoDB Local answers DynamoDB's, so a bucket and a
data store are reached through the deploy target's own provider with the
endpoint pointed elsewhere, which is all the other SDKs do too. SQS and SNS have
no such stand-in, so those two are served locally. `resources/local` is a
provider whose `Detect` is `PlatformLocal`, serving those two and handing the
other four to `resources.FallbackProvider`. Exactly one of the two providers
detects at a time, which is what keeps the choice from depending on the order
the generated file imports them in.

**What serves a local queue is a backend, not the provider.** The provider
decides which kinds a session serves; what carries them is registered through
`resources/local/internal/backend`, the same arrangement the AWS module's
`internal/service` is, for the same two reasons: a session whose blueprint
declares no queue links no Redis client for one, and a backend that is not Redis
can be added without the provider learning about it. The provider imports
nothing Redis, and a build that forgets the backend gets an error naming the
import rather than a nil client. `generate --local` writes both.

The backend is `resources/local/redis`, because the runtime reads a local queue
as a Redis stream and a local topic as a Redis channel that `local-events`
relays into one stream per consumer. The stream key, the field names, the
`message_type` of 0 or 1 and the topic envelope's keys are a contract with
another program, the same way the handler tags are, so its integration suite
asserts them by reading a real Valkey back the way the runtime reads it.

**A bucket reaches the versions a blueprint told it to keep.** Versioning is
available in v0 on all three clouds and no SDK could reach it. `Get` and `Info`
take a version, `Delete` removes one for good rather than writing a marker, and
`Versions` lists them. S3 answers a version listing with the versions and the
delete markers in two lists and this merges them into one, newest first per key,
because a marker is a version as far as a handler is concerned and what it wants
is the newest one that is not a marker. `NoSuchVersion` joins `NoSuchKey` and
`NotFound` as an absence: they are three answers from S3 and one answer to a
handler, which an integration test caught rather than a unit test.

**`DeleteMarker` is the one field the portable contract lets a provider own.**
S3 records a versioned delete by writing a marker as the new current version;
Cloud Storage and Blob Storage archive the current version instead, so neither
has markers and the field is constant false on both. Accepted once, because
without it the S3 restore path cannot be expressed, and meant to stay the only
one: a second always-false field would mean the contract had started describing
a store rather than object storage. Everything else that differs stays behind
the call, which is where the delete-request ceiling, a copy that takes more than
one request, and whether a signed upload URL can bind a content type all live.

A read of a marker is an absence, which is not obvious from S3's own answer: it
replies to a read of one with 405 rather than 404. `Versions` hands marker ids
back, so a caller passing one straight in is the ordinary case rather than a
hypothetical, and before this was mapped `Exists` answered false *and* an error
at the same time. Only on a read: a 405 from anything else is still a failure.

**A resource is reached through a client built for its own region.** Every AWS
resource a deployment creates for an application is in the application's region,
which is why one client per service was enough. A resource the blueprint declares
as external may be anywhere, and a service routes a request by the region its
client was built for, so `internal/service` holds a `Clients[T]` keyed by
`ClientKey` and each kind asks for the client its `Ref` names. The region is read
off the resource's own config key under `resources.FieldRegion`, the same way a
cache reads `_host`: absent means the application's own, so every existing
deployment behaves as before and the deploy engine can start writing the key
whenever it is ready. The key is a struct rather than a region string because
assuming a role in another account would key on the identity the client signs
with too, and that is a field rather than a second cache. An S3 bucket ARN
carries no region, which is why a bucket cannot infer one and the deployment has
to record it. `CopyObject` is the call that forced this: it names two buckets and
S3 routes it to the one being written to, so a copy is sent with the
destination's client rather than the source's. `PermanentRedirect` and
`AuthorizationHeaderMalformed` are mapped to an error naming the bucket and the
missing region, since neither reads as a region mistake on its own. Node and
Python do all of the same; the transformer's part is planned in bluelink-transformer-celerity's
own design docs.

**A span is the contract's, not the provider's.** Every provider's operations
are the same operations, so tracing wraps what a provider built rather than
being each provider's to do: the six typed accessors in `resources/handles.go`
return a tracing decorator, which is why AWS, a development session and a test
double all produce one span name and one attribute set. Span names follow the
Node and Python SDKs rather than Go's method names, so a listing is
`celerity.bucket.list_page` here as there although the method is `List`, and
`celerity.queue.send_message` although the method is `Send`. One trace reads the
same whichever language served the request. The dispatch span is opened by the
outermost pipeline layer, so it covers the guards and every layer and
everything a handler traces is a child of it; the arriving trace context is
attached before it opens, so a trace that started upstream continues into it.
`telemetry.Traced` hands the span to the call as well as the context, because a
batch's counts are not knowable until the call returns. The cache's wrapper is
fifty-three delegations written out, and what keeps them honest is a test rather
than a generator: a missing method is a compile error through the assertion in
`traced.go`, but one delegating to its neighbour or naming the wrong span
satisfies the interface just as well, and a handful asserted by hand leaves the
rest unchecked. So every operation on the contract is reflected over, called,
and the double underneath asked whether it saw the same call under the matching
span name, which covers an operation the day it is added rather than when
somebody remembers to write a case for it. `Scan` and `TTL` are written out
separately, the first because a walk yields rather than finishing at one moment
and the second because three results do not fit `Traced`.

**A test binary links the providers because the build writes its imports, not
the developer.** The whole point of `generate` is that the application names no
platform, and a test package hand-writing blank imports would put that back in
the one place it is least visible. So `--test-packages` writes them there too.
Three things make it safe to write into source: which packages get the file is
the type checker resolving `celeritytest.Live` to the SDK's own function rather
than a selector matched in the text, since an application with its own `Live`
would otherwise collect a cloud SDK it never asked for; the file is always named
`_test.go` and carries the suite's build tag, so an ordinary `go test` links none
of it and a deployed artefact cannot; and the contents come from the same
`GeneratePlatformFile` call as the main file, so there is no second list of
imports to drift. A package that does not type-check is an error rather than a
package that calls nothing, for the reason extraction reports a load failure:
writing to the packages that happened to load leaves the rest to fail later and
further from the cause. `go mod tidy` runs before the scan, because the file just
written imports modules the application does not require yet and the test
packages cannot be read until they resolve.

**An edge integration test reuses the local provider rather than building
clients.** Node and Python answer `integration: true` by constructing a client
per resource kind from the endpoints `celerity dev test` exports, because
neither has a provider object to reuse and both discover resources from runtime
DI metadata. Go has `resources/local`, which is already that provider: it serves
a queue and a topic and hands the other four to the platform's, and the AWS
provider takes its endpoint from `AWS_ENDPOINT_URL_*` through plain
`LoadDefaultConfig`, so pointing S3 and DynamoDB at MinIO and DynamoDB Local is
environmental and there is nothing to write. So `celeritytest.Live` finds the
linked provider through `resources.DetectedProvider` and delegates, which also
keeps core from importing a provider module: the test's own blank imports
register it. A second implementation of endpoint resolution would be a second
thing to drift, which is what the cache span names did.

Mixing is per resource and needs no `With` method per kind: a double asked for
by name before the application takes its handles is used for that resource and
the live provider is not asked. Asked for afterwards it is refused, because a
handle is taken during registration and held, so a double made later is one
nothing reaches and a test reading it would conclude the handler did nothing.
That ordering does not matter under `Resources()`, where there is one object per
name either way, so the guard is only armed for live resources.

**A SQL database has no test double, and saying so is the feature.** The
harness fakes a bucket, a queue, a topic and a data store because a write
followed by a read is the whole contract, and stubs a cache because the surface
is large and recording the calls is honest about what it checks. A database is
neither: what a handler has to be right about is the statement it sends, and
only an engine can judge that. The stub that existed recorded that a connection
was asked for, which is a test that passes with the query misspelt, so it is
gone. An unsupplied database refuses the call and names the way to test it, and
`Provider.WithDatabase` takes a real client: the engine a `celerity dev` session
runs, or whatever a suite starts for itself. The `WriterFunc` seam went with it,
since a hook that hands back a connection invites reimplementing an engine
behind it, which is the thing not worth doing.

**The tracer is installed, not injected.** `telemetry.SetTracer` is
process-wide, for the same reason a resource provider is registered rather than
constructed: which implementation is linked is the build's decision and the
same for every dispatch, so threading it through every resource handle would be
plumbing that never varies. Absent one, the seam answers with a tracer that
records nothing, so a resource traces unconditionally and an application that
exports nothing pays a call rather than a branch at every site. `telemetry/otel`
resolves OpenTelemetry's provider per call rather than capturing it at install
time, because an application commonly sets its provider after init has run and a
captured tracer would keep writing to the no-op that preceded it. It configures the
exporter from the environment rather than leaving it to the application, which
is what Node and Python do and what an operator expects: the same variable
names, the same defaults, OpenTelemetry's own read before Celerity's so that
`OTEL_EXPORTER_OTLP_ENDPOINT` works here as it does anywhere. Nothing is built
until `CELERITY_TELEMETRY_ENABLED` is true, so an application that traces
nothing opens no connection. An application wanting its own provider opts out of
nothing: the tracer resolves the provider per call and an init runs before main,
so whatever it installs later wins, which is a cleaner arrangement than Node's
and Python's, where the SDK's setup is a function you must not call. A collector
URL that will not parse is refused by name, since the exporter connects lazily
and would otherwise accept a typo now and export nowhere later.

A batch of spans is held so that exporting is not a request per span, so
something has to hand it over. `telemetry.SetFlusher` is the seam, installed the
way the tracer is, and two places ask: `celerity.Run` on a runtime shutdown,
before the pools are given back since an exporter has a connection of its own;
and the serverless dispatcher per invocation, deferred so the failure paths are
covered too. Per invocation rather than on a signal, because that window is the
one thing a platform guarantees, which is the point `run.go` already made about
buffered telemetry being what earns a share of a shutdown. `--telemetry` is its own flag rather than derived from the
deploy target, since an application deployed to AWS may export anywhere, and an
unknown value is refused by name because an application that asked to be traced
and was not is the hardest kind of missing telemetry to notice.

**A readable record is a line and its attributes beneath it.** One line per
record was the first shape and it does not survive a real dispatch: the trace,
the handler and the route are there before the application has added anything,
and a dozen attributes on one line is 450 characters that wraps across a
terminal into something nobody can scan. So the attributes are indented under
the message, which is the shape the CLI's own `devlogs` formatter renders a
session's logs in, and an application run under the CLI and one run directly
read the same way. The trace leads and the rest is sorted: sorted so the same
record reads the same way twice, the trace lifted out of that because it is what
gets copied when a record starts an investigation and alphabetically it would
sit among the application's own keys. The runtime's own pretty format was
considered as the model and is not one for this: `tracing_subscriber`'s Pretty
is multi-line, but what it puts on the later lines is the span and source
location, leaving the event's own fields on the first, so adopting it would
leave a twelve-field record exactly as long. What was worth taking from it is
`key: value` over `key=value`. Node reaches the same shape through pino-pretty
and Python does not, rendering one line through structlog's ConsoleRenderer,
which has the problem this fixed.

**Logging is slog, and the trace is bound into the logger.** The seam is
`*slog.Logger` rather than an interface of its own, so any handler an
application already has works and core carries no logging dependency; zap was
considered and left out for that reason, since zap behind `zapslog` is a handler
an application can pass in. What the SDK supplies by default is JSON, because a
deployed application's records are read by an aggregator, and one line per
record in a development session, because there they are read by a person as they
scroll past. `CELERITY_LOG_LEVEL` and `CELERITY_LOG_FORMAT` are the names the
Node and Python SDKs read, so one deployment configures every language alike.
The span is started before the logger is attached, which is what lets a handler
write a correlated record with `logger.Info` rather than `logger.InfoContext`:
slog's plain methods log against a background context, so a correlating handler
would see no span. Absent a recording trace the fields are left off rather than
written empty. There is no OTel Logs export: Python has none either, and a
deployment feeding its platform's log export into a monitoring stack is the
ordinary arrangement.

**Backend instrumentation is per backend, and AWS is ours.** The contract-level
span says what the application asked for; the backend span says what was
actually asked, which is where a call slow because of three retries stops
looking like one slow call. Each backend is reached differently, so each is
instrumented differently. A cache goes through `redisotel`, which the go-redis
authors maintain themselves, in a module linked where a cache is declared. A
database goes through `XSAM/otelsql`, which is community rather than official
because OpenTelemetry's contrib ships no `database/sql` instrumentation at all;
instrumenting `database/sql` means wrapping the driver, so `sqldb.Open` takes a
connector rather than handing back a pool, and the AWS provider routes both its
fixed-DSN and its IAM path through it. AWS is written here: OpenTelemetry's own
`otelaws` was deprecated in October 2026 for want of a maintainer, and the
compile-time successor, `otelc`, is a `go build` wrapper rather than a package
and routes through the deprecated one anyway. What was needed is a middleware
against an extension point the AWS SDK documents, emitting through the telemetry
seam rather than to OpenTelemetry directly, which is why it adds no dependency,
needs no module, and needs no registration hook either: `redis` and `sqldb`
have an `Instrument` because their instrumentation is a separate module that has
to reach back in, and AWS had one too until it was removed unused. The
middleware is appended to the ambient configuration where that is loaded, once,
so every client built from it afterwards traces without asking. Since the AWS module already
has the SDK and core, a test's recorder sees the spans too, and what it costs an application exporting nothing
is the same no-op tracer everything else goes through. Adopting `otelc` in the
CLI would cover `database/sql` and `net/http` officially and is worth revisiting
once the CLI owns the Go build.

**Shutdown gives back the pools, and only in the runtime.** `resources.Closer` is
asserted for rather than part of `Provider`, since most resources are HTTP
clients a process can simply stop using; a SQL pool and a cache's sockets are
not. `celerity.Run` calls `resources.Release` when the runtime stops serving,
with a context of its own because the one that was serving is cancelled by the
signal that asked for the shutdown. Nothing calls it on a serverless platform,
where an environment is frozen between invocations and torn down rather than shut
down, and a pool given back now is one the next invocation rebuilds. A pool is
read for closing through an `atomic.Pointer` rather than through the `sync.Once`
that builds it, so a shutdown racing a first query neither consumes the Once nor
reads a half-built pool.

**A resource handle resolves on first use, not when it is taken.** Handles are
taken during registration, before an event has arrived; resolving there would
put a config store request, and on a serverless platform a cold start, in the
middle of a list of registrations. A provider is handed a `resources.Ref` and
asks it when a handler first calls. Most resources are one identifier under the
links file's config key; a cache or a database is a family of values under that
key with suffixes (`_host`, `_port`, `_authMode`, ...), read through `Ref.Field`.
The suffixes match what the Node and Python SDKs read, so the deploy engine
writes one shape for every language.

**The SDK imports no SQL driver; the build writes the import.** `database/sql`
takes one from whatever the program imported, and compiling every supported
driver into every application that touches a platform is a cost paid by
applications with no database. Which one is a deployment fact, so it is decided
where the platform is: `generate --sql-engine postgres,mysql` writes a blank
import per engine, and `engine=import/path` names a driver of your own. The
engine names are written out in `cmd/celerity-go` rather than imported from
`resources`, the way the deploy targets are, since that module requires a
released core. A missing driver is an error pointing at the flag first and the
hand-written import second.

**Resource wiring is a two-pass extraction.** Running the binary reports what it
serves; a static pass over the source resolves which resources each handler
reaches, because that dependency is captured in a closure and Go offers no
reflection into one. Resource names are constant-folded, and a non-constant one
fails extraction rather than silently dropping an IAM grant; `celerity.Uses` is
the additive escape hatch.

Both passes are implemented. The static pass is rooted at the manifest's
`FuncPath`, which is what the runtime calls the registered function, so the
analysis is tied to the function the binary will dispatch to rather than to
whatever the source looks like. Reading that name is most of the work:
`runtime.FuncForPC` spells a method value `main.(*orders).create-fm`, where the
dot after the receiver separates a type from its method rather than one step
from the next and `-fm` is the compiler's wrapper around the same body, and a
function literal `main.main.func1`, numbered in source order within its
enclosing body. The main package is `main` whatever the module is called, which
is not the import path `go/packages` keys it by. Each of those was a bug found by
running extraction against a real application rather than a fixture that only
looked like one, which is why `testdata/app` is a module that compiles and runs.

What the walk looks for is references to a handle rather than calls to a
resource: a handler does not call `resources.Bucket`, it closes over what that
returned or reaches it through its receiver. So handles are collected to a fixed
point, because one arrives at an object through another: `&orders{store: store}`
stores a variable, not the call, and which of the two the source mentions first
is the author's business. Calls into the application's own functions are
followed, since a handler whose work is done by a service it holds reaches what
that service reaches. A failure to load the source is reported rather than
treated as an application that reaches nothing, which would grant every handler
access to nothing and fail on the first real event.

**A handler reaches its resources indirectly, so the walk is interprocedural in
three ways.** Following calls was there from the start; the other two were found
by running extraction against an application written the way Go is ordinarily
written rather than the way the first fixture was. A handle passed as an
argument is noted against the parameter it arrives as, because a constructor
stores its parameter rather than the variable the caller named and without that
`newShipping(outbox)` leaves the field holding nothing the walk can see. An
interface method names no body, so the application's own implementations of it
are walked instead: a service behind an interface is ordinary Go, and the call
site names the interface. That over-reports where an interface has more than one
implementation, which is the safe direction, since a resource reported that a
handler cannot reach grants access nobody uses while one missed is a handler that
fails on its first real event. Test doubles do not widen it, because the packages
loaded are the application's own and its test files are not among them.

A constructor that can fail answers two values, which is one expression on the
right of the assignment and two names on the left, and collection used to skip
anything uneven. Setting a resource up commonly has work to do first, a
connection to establish or session credentials to obtain, so this is the
ordinary shape rather than an edge of one. Where the handler reaches a field the
constructor stored, binding the argument to the parameter already resolved it;
where it reaches what the call answered, nothing but the return statement
connects the two, so what a call returns is read from the function's returns.
Parameters are bound by then, so a wrapper returning the client it was given
resolves, and the fixed point carries it to whatever the result was assigned to.
A function returning a call to itself is guarded against rather than followed.

A factory is often not named at the call site. It is assigned to a variable and
called through it, or it answers with a closure that is called through one, and
either way the call names a variable rather than a declaration. So a func-typed
variable is resolved to the function behind it, in two kinds: an alias, where
calling the variable is calling that function and its parameters bind to the
arguments; and a variable holding what a call returned, where the body to walk
is the factory's, since the literal that captured the handle is declared inside
it, but the arguments belong to the closure and bind to nothing. Keeping those
apart matters: binding a closure's arguments to the factory's parameters would
record a resource the factory never saw. Nested factories, a factory answering
with an interface, and a generic factory all worked without this, which is why
the two that did not were worth finding.

What an object holds is a set rather than one resource. A field assigned a
different bucket by two constructions holds either, and a parameter is whatever
its callers pass; keeping the last would drop one, so the fixed point counts
references rather than objects to know it is still making progress.

Erroring on an unresolvable interface call was considered and rejected: a handler
calling `h.clock.Now()` on an interface of the application's own reaches no
resource, and failing extraction over it would refuse to build a correct
application. `celerity.Uses` remains the declared way to name what the walk
cannot see.

**The generated stubs are committed.** Same reasoning as the runtime's Rust
stubs: a build needing a code generator needs it in every matrix leg of every
release workflow. CI fails on drift.

