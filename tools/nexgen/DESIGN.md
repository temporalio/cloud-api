# protoc-gen-nexgen — Design

> **Status:** M1–M3 implemented (§10); M0's end-to-end handler call and M4
> are not started. The nexgen questions M0 was meant to answer were measured
> against the pinned nexgen (§11), and decisions D8, D12, and D13 were revised
> to match (§2).
> **Target nexgen:** commit `b80f08d` (v0.2.7, no release yet), pinned as
> `NEXGEN_REV` in the Makefile.
> The nexgen subset is still unstable, so the [open questions](#11-open-questions)
> must be checked against that pinned version.

## 1. Goal

Turn the Temporal Cloud API protos into nexgen definition files. nexgen then
generates Go, Java, Python, and TypeScript models plus Nexus bindings for
`CloudService` from those files. The protos remain the single source of truth,
and the definition files are a mechanical projection of them.

### Non-goals

- Changing the protos to suit nexgen. Gaps are handled by the mapping or listed
  as known divergences.
- Running nexgen itself to produce language code. That belongs downstream, and
  CI here only smoke-tests that nexgen accepts the output.
- Projecting HTTP/OpenAPI annotations (`google.api.http`, `openapiv2_*`). They
  are ignored.
- Expressing validation the protos don't declare. The repo has no
  `buf.validate` or `field_behavior` annotations, so the schemas carry type
  shape only.

## 2. Decision log

Decisions agreed during design review (2026-10-06). The rejected alternatives
are listed so they aren't reopened accidentally.

| # | Topic | Decision | Rejected alternatives |
|---|---|---|---|
| D1 | Wire format | **Canonical protojson.** Property keys are `json_name`, int64 is a string, Duration is `"1.5s"`, Timestamp is RFC 3339, enums are name strings. | nexgen-idiomatic JSON, which would need a translation layer; a configurable mode, which would double the test surface. |
| D2 | Scope | **Service closure.** Every RPC of each service becomes an operation, and only the messages and enums reachable from request/response types are emitted. | An allowlist of RPCs; emitting all messages. |
| D3 | Layout | **One file per proto package.** Each file has its own `$defs`, the service package becomes a `.nexusrpc.yaml`, and references between files are relative `$ref`s. | A single file, which collides on `State`, `Summary`, `Health`, and others; one file per `.proto`. |
| D4 | `oneof` | **Optional properties with a documented exclusivity note.** The validator does not enforce at-most-one. | JSON Schema `oneOf` with required-key branches, which nexgen may not accept; failing generation. |
| D5 | Required | **Nothing is `required`**, which matches proto3 presence semantics. | Parsing `// Required.` comments; `google.api.field_behavior`. |
| D6 | Strictness | **Open objects**: `additionalProperties` is left unset. | Closed objects, which would break old readers whenever a field is added. |
| D7 | Deprecation | **Emit deprecated fields with `deprecated: true`.** | Dropping them; a plugin flag. |
| D8 | Enums | **Exact value names, including `*_UNSPECIFIED`.** *Revised:* the `x-go-enum-names` / `x-java-enum-names` overrides are gone, because enums are no longer emitted as `enum` (D13). | Dropping UNSPECIFIED; no overrides. |
| D9 | Awkward types | **Strings with patterns** for 64-bit ints and Duration; Timestamp uses `format: date-time`; bytes use base64; Struct is a free-form object; Any is an open object requiring `@type`; Payload is emitted as a normal message. | An opaque escape hatch; hard-failing. |
| D10 | Implementation | **Go with [protoc-gen-star](https://github.com/lyft/protoc-gen-star) v2** (`github.com/lyft/protoc-gen-star/v2`) in its own module under `tools/nexgen`, run as a buf local plugin from a separate `buf.gen.nexgen.yaml`. | Plain `google.golang.org/protobuf/compiler/protogen`; adding it to `buf.gen.yaml`; Rust. |
| D11 | Output | **Committed under `nexusrpc/`** at the repo root. CI fails if the files drift from the protos. The directory is not called `nexus/` because that would produce paths like `nexus/temporal/api/cloud/nexus/v1/nexus.yaml`. | Writing only to `.gen/`; naming the directory `nexus/`. |
| D12 | Naming | **Proto names are kept on the wire.** The service key is `CloudService`, the service `fqn` is the proto full name, and property keys are `json_name`. *Revised:* nexgen requires operation keys to match `^[a-z][a-zA-Z\d]+$`, so the key is the lowerCamel RPC name (`getNamespace`) and the operation's `fqn` is the RPC name (`GetNamespace`), which keeps the wire name. | RPC names as keys (rejected by nexgen). |
| D13 | Open enums | **Enums accept unknown values**, so a client generated before a value was added still accepts responses that carry it. *Revised:* nexgen rejects `anyOf`, allows only objects in `$defs`, and rejects unknown `x-*` keywords, so the planned `anyOf` def can't be expressed. Each enum use is an inline `type: string` whose `description` lists the known names (§5.4), which is the fallback this doc planned for Q8. Generated code has no typed constants. | Closed `enum`s, which give typed constants but make every new enum value a breaking change for older clients. |
| D14 | Handler codec | **Operation handlers decode and encode with protojson.** The SDK's default payload converter is not used for these operations (§5.7). This is what makes D1 hold end to end. | Relying on the SDK's default data converter; a nexgen-shaped DTO layer in the handler. |
| D16 | Flat type namespace | **A `$defs` key used in more than one package gets `x-go-name` / `x-py-name` / `x-ts-name` of its package segment plus the key** (`NamespaceLifecycleSpec`). nexgen puts every type in one namespace for Go, Python, and TypeScript; Java keeps a package per file. An inline object property whose synthesized name collides with a def is moved into `$defs` (§6.4). | Renaming every def by package, which would churn every generated name. |
| D15 | Filtering | **A checked-in filter file excludes RPCs and fields** (§5.8). Exclusion happens before the closure, so types reachable only through excluded elements are pruned too. Every rule must match something. The first use removes all async-operation surface: `GetAsyncOperation`, the `async_operation_id` fields, and the `AsyncOperation` response fields. Nexus operations return only after the underlying async operation completes (§5.7). | Repeated plugin options, which PG\*'s `Parameters` map can't hold (for the same reason, `services` separates names with `+`: PG\* splits the whole option string on `,`); proto custom options such as `(nexgen.exclude)`, which would change the protos (a non-goal); dropping the types after the closure, which would leave dangling `$ref`s. |

## 3. Architecture

The plugin is a single PG\* `Module`. `main.go` is:

```go
func main() {
	optional := uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
	pgs.Init(
		pgs.DebugEnv("DEBUG_NEXGEN"),
		pgs.SupportedFeatures(&optional),
	).RegisterModule(module.New()).Render()
}
```

The module embeds `*pgs.ModuleBase`. Its `Execute(targets map[string]pgs.File,
pkgs map[string]pgs.Package) []pgs.Artifact` runs this pipeline:

```
PG* AST (pgs.File / Service / Method / Message / Field / Enum, comments loaded)
  │
  ├─ 1. options      m.Parameters(): services=, out_format=, filter=; load the filter file
  ├─ 2. mapping      pgs.Walk every package with a Visitor → registry of schema nodes
  │                  keyed by proto FQN, one per pgs.Message and pgs.Enum (§5);
  │                  Message.IsWellKnown()/WellKnownType() routes WKTs to inline
  │                  mappings, so a WKT field never produces a ref
  ├─ 2b. filter      drop excluded fields from nodes and excluded methods from
  │                  services (§5.8); fail if any rule matched nothing
  ├─ 3. closure      mark and sweep over the registry: mark the Input()/Output() refs
  │                  of every selected, non-excluded method, follow each node's refs
  │                  (properties, items, additionalProperties) transitively, then
  │                  drop everything unmarked. Imported files such as
  │                  temporal.api.common.v1.Payload are included automatically.
  ├─ 4. naming       $defs keys, output file per pgs.Package, keyword overrides (§6)
  ├─ 5. assemble     group nodes by package → document per file; resolve $refs
  │                  to "<relative path>#/$defs/<Key>" or "#/$defs/<Key>"
  └─ 6. emit         deterministic YAML/JSON → m.AddGeneratorFile(path, content)
```

How each PG\* feature is used:

| Need | PG\* API |
| --- | --- |
| Plugin parameters | `m.Parameters().Str("services")`, `.Str("out_format")`, `.Str("filter")` |
| Filter matching (§5.8) | `field.FullyQualifiedName()`, `method.FullyQualifiedName()`, `field.Type().Embed().FullyQualifiedName()` |
| Comments for `description` (§5.5) | `entity.SourceCodeInfo().LeadingComments()` / `.TrailingComments()` |
| JSON name | `field.Descriptor().GetJsonName()` |
| Field shape | `field.Type()`: `IsRepeated()`, `IsMap()`, `IsEmbed()`, `IsEnum()`, `ProtoType()` |
| oneof (D4) | `field.InRealOneOf()`, `field.OneOf().Fields()`. A proto3 `optional` field sits in a synthetic oneof, which `InRealOneOf()` excludes. |
| Nested type names | `msg.Parent()` chain, or `msg.FullyQualifiedName()` minus the package |
| Enum prefix stripping (§5.4) | `naming.EnumValuePrefix(enum.Name())`, `value.Name()`. PG\*'s `Name.UpperSnakeCase()` returns `Resource_State` and `ScreamingSnakeCase()` splits digits into their own words, so neither matches buf lint's prefix. |
| Deprecation | `field.Descriptor().GetOptions().GetDeprecated()` |
| Diagnostics | `m.AddError(...)`, which sets `CodeGeneratorResponse.error`. protoc and buf print it and write no files. `Failf`/`CheckErr` are not used because they exit the process. |
| Output | `m.AddGeneratorFile(name, content)`, which writes through the `CodeGeneratorResponse` so buf's `out:` controls the location. `AddCustomFile` is never used because it writes to disk directly. |

**Why map first, then prune.** The closure is computed from the `$ref`s the
mapping actually emitted, not from a separate walk over descriptors. As a
result, the emitted set and the referenced set always match. When the mapping
inlines a type (a WKT) or rejects one, or the filter removes the field that
referenced it, the closure reflects it without a second copy of the type rules. Building a node for every message in the request costs
very little: the closure covers a few hundred messages. The pattern comes from
[pubg/protoc-gen-jsonschema](https://github.com/pubg/protoc-gen-jsonschema)'s
`2_backend_optimizer.go`. Our implementation keeps the marks in a separate set
rather than on the nodes, so nothing can leak into the output.

Diagnostics are only raised for nodes that survive the sweep. An unsupported
type in a message the service never uses must not fail generation. The mapping
therefore records an error on the node, and the plugin reports it only if that
node is marked.

Using `AddGeneratorTemplateFile` with `text/template` was considered and
rejected. The output is a data document, so the plugin builds an ordered node
tree and marshals it with `gopkg.in/yaml.v3` (`yaml.Node`, so key order and the
header comment are controlled). Templates would make it hard to get indentation
and escaping right.

- **buf strategy.** `buf.gen.nexgen.yaml` must set `strategy: all`. Under the
  default `directory` strategy, buf calls the plugin once per directory, so
  `Execute` would see one package at a time and couldn't compute the closure or
  the cross-package `$ref`s. PG\* builds its AST from every file in
  `CodeGeneratorRequest.proto_file`. `targets` holds only `file_to_generate`, but
  entities in dependency files such as `temporal/api/common/v1` can still be
  reached through `Field.Type().Embed()`, so the closure walk crosses into them.
- **Determinism.** Output files are written in package-name order. Within a
  file, `$defs` entries are ordered by source file path, then by declaration
  order within the file, with each nested type placed directly after its parent
  (a pre-order walk, so `CodecServerSpec`, `CodecServerSpecCustomErrorMessage`,
  `CodecServerSpecCustomErrorMessageErrorMessage`, then the next top-level
  type). Properties follow field-number order (§5.2), and enum values follow
  declaration order. Keys inside a node are written in the fixed order
  `description`, `deprecated`, `type`/`$ref`, type-specific keywords,
  then `x-*` extensions sorted by name. The same input always gives
  byte-identical output, which `nexgen-check` depends on.
- **Diagnostics.** An unmappable construct in a type that is reachable from a
  selected service fails the whole run. The error has the form
  `file.proto:line: <Message>.<field>: <reason>; <fix-it>`, for example
  a `google.protobuf.Value` field or a map with a message key. Every reachable
  error is reported, not just the first, and nothing reachable is silently
  skipped. Unreachable types never produce diagnostics (see above).

## 4. Output files

| Package | File |
| --- | --- |
| package containing a service | `nexusrpc/<pkg path>/<segment>.nexusrpc.yaml` |
| any other package in the closure | `nexusrpc/<pkg path>/<segment>.yaml` |

`<pkg path>` is the package with dots turned into slashes
(`temporal/api/cloud/namespace/v1`), and `<segment>` is the last segment before
the version (`namespace`). `google.protobuf.*` is never emitted. Every
well-known type is inlined (§5.3).

Each file starts with a header that has one `# source:` line for every
`.proto` file in the package that contributed a def, sorted by path. When a
filter is in use, a `# filter:` line names it, so a reader knows the file is not
the whole proto surface:

```yaml
# Code generated by protoc-gen-nexgen. DO NOT EDIT.
# source: temporal/api/cloud/cloudservice/v1/request_response.proto
# source: temporal/api/cloud/cloudservice/v1/service.proto
# filter: nexgen.filter.yaml
```

A schema file then continues with `$schema` and `$defs`. A Nexus document
continues with `nexusrpc`, `$schema`, `services`, and `$defs`.

Package schema files have no root type, only `$defs`. nexgen accepts that and
resolves `file.yaml#/$defs/X` (Q1), as long as it is given the whole
`nexusrpc/` directory as input; a `$ref` that leaves the input root is
rejected.

### Example (abridged)

The defs, properties, and enum values are cut down, but the layout is what
the plugin emits. Nodes use block style; the only flow-style value is a
`required` list.

`nexusrpc/temporal/api/cloud/cloudservice/v1/cloudservice.nexusrpc.yaml`

```yaml
# Code generated by protoc-gen-nexgen. DO NOT EDIT.
# source: temporal/api/cloud/cloudservice/v1/request_response.proto
# source: temporal/api/cloud/cloudservice/v1/service.proto
# filter: nexgen.filter.yaml
nexusrpc: "1.0.0"
$schema: https://json-schema.org/draft/2020-12/schema
services:
  CloudService:
    fqn: temporal.api.cloud.cloudservice.v1.CloudService
    description: |-
      WARNING: This service is currently experimental and may change in
      incompatible ways.
    operations:
      getNamespace:
        description: Get a namespace
        fqn: GetNamespace
        input:
          $ref: "#/$defs/GetNamespaceRequest"
        output:
          $ref: "#/$defs/GetNamespaceResponse"
$defs:
  GetNamespaceRequest:
    type: object
    properties:
      namespace:
        description: The namespace to get.
        type: string
  GetNamespaceResponse:
    type: object
    properties:
      namespace:
        description: The namespace.
        $ref: "../../namespace/v1/namespace.yaml#/$defs/Namespace"
```

`nexusrpc/temporal/api/cloud/namespace/v1/namespace.yaml`

```yaml
# Code generated by protoc-gen-nexgen. DO NOT EDIT.
# source: temporal/api/cloud/namespace/v1/message.proto
# filter: nexgen.filter.yaml
$schema: https://json-schema.org/draft/2020-12/schema
$defs:
  CodecServerSpec:
    type: object
    properties:
      endpoint:
        description: The codec server endpoint.
        type: string
      customErrorMessage:
        description: A custom error message to display for remote codec server errors.
        $ref: "#/$defs/CodecServerSpecCustomErrorMessage"
  CodecServerSpecCustomErrorMessage:
    type: object
    properties:
      default:
        description: The error message to display by default for any remote codec server errors.
        $ref: "#/$defs/CodecServerSpecCustomErrorMessageErrorMessage"
        x-java-name: default_
  Namespace:
    type: object
    properties:
      state:
        description: |-
          The current state of the namespace.

          One of the `ResourceState` values:

          - `RESOURCE_STATE_UNSPECIFIED`
          - `RESOURCE_STATE_ACTIVATING`
          ...

          Newer servers may return values not listed here.
        type: string
      stateDeprecated:
        deprecated: true
        type: string
      createdTime:
        type: string
        format: date-time
```

`x-java-name: default_` and `x-ts-name: default_` are there because
`default` is reserved in Java and TypeScript (§6.3). There is no
`resource.yaml`: that package holds only enums, which are inlined (§5.4).

## 5. Type mapping

### 5.1 Services and operations

| Proto | Output |
|---|---|
| `service S` | `services.S` with `fqn: <package>.S` and `description` taken from the leading comment |
| `rpc M(Req) returns (Resp)` | `operations.m` (lowerCamel) with `fqn: M`, `input: $ref Req`, and `output: $ref Resp` (D12) |
| an RPC name whose lowerCamel form doesn't match `^[a-z][a-zA-Z0-9]+$` | **error**, such as a one-letter name |
| `google.protobuf.Empty` as input or output | an inline empty object; any other well-known type there is an **error** |
| client or server streaming RPC | **error**: Nexus operations are unary |
| `option deprecated = true` on a service or RPC | `deprecated: true` (Q5) |
| a service whose RPCs are all excluded by the filter | **error**: nexgen rejects a service with no operations |

Request and response messages are always objects, so they satisfy nexgen's rule
that operation input and output must be objects. An empty request message still
gets a def rather than an omitted `input`, which keeps the contract stable if a
field is added later. nexgen requires every object to declare its shape, so an
object with no fields is written `type: object` with `properties: {}`.

### 5.2 Messages and fields

- A message becomes `type: object` with `properties` in field-number order. It
  has no `required` and no `additionalProperties` (D5, D6).
- Property keys are the field's `json_name`, as protojson emits them.
- **Presence.** A field with proto3 `optional` is mapped the same way as any
  other field. The decision to require nothing (D5) means presence doesn't change
  the schema today. The plugin still classifies each field so that a later move
  to field-level `required` or nullable needs no new rules:
  - *Real oneof member*: `field.InRealOneOf()`. Never required. Gets the
    exclusivity note below.
  - *Explicit presence*: `field.HasPresence() && !field.InRealOneOf()`. This
    covers proto3 `optional`, singular message fields, and every proto2 field.
    protojson omits the field when it is unset.
  - *Implicit presence*: everything else, meaning proto3 scalars, repeated fields,
    and maps. protojson omits zero values, so the field can't be required even
    though it is always "set".

  The classification follows protobuf's
  [`implementing_proto3_presence.md`](https://github.com/protocolbuffers/protobuf/blob/main/docs/implementing_proto3_presence.md#to-test-whether-a-field-should-have-presence).
  Do not check `HasOptionalKeyword()`, which misses message-typed fields.
- Nested messages and enums are flattened into the package's `$defs`, with the
  parent names concatenated: `CodecServerSpec.CustomErrorMessage.ErrorMessage`
  becomes `CodecServerSpecCustomErrorMessageErrorMessage`.
- `$ref` with sibling keywords is valid in 2020-12, but nexgen treats a `$ref`
  with a `description` beside it as a new type and generates a copy
  (`RespThing` instead of `Thing`). `deprecated` beside a `$ref` keeps the shared
  type. So a property that is a `$ref` carries `deprecated` and no
  `description`, and the field's comment is lost (Q2).
- **`oneof` (D4).** Each member is emitted as an ordinary optional property, and
  every member's description gets this paragraph appended:
  `Mutually exclusive with <other json names> (oneof \`<name>\`).` A member
  that is a `$ref` loses it with the rest of its description.
  At-most-one is not enforced; this is listed as known divergence K1.
- Messages that reference themselves directly or through other messages use
  `$ref`. nexgen accepts cycles through both array edges and optional message
  fields (the `cycles` test case). The closure walk logs the cycles it finds
  with `DEBUG_NEXGEN` set.

### 5.3 Scalars and well-known types

| Proto | Schema |
|---|---|
| `string` | `type: string` |
| `bool` | `type: boolean` |
| `int32`, `sint32`, `sfixed32` | `type: integer`, `minimum: -2147483648`, `maximum: 2147483647` |
| `uint32`, `fixed32` | `type: integer`, `minimum: 0`, `maximum: 4294967295` |
| `int64`, `sint64`, `sfixed64` | `type: string`, `pattern: "^-?[0-9]+$"` |
| `uint64`, `fixed64` | `type: string`, `pattern: "^[0-9]+$"` |
| `float`, `double` | `type: number` |
| `bytes` | `type: string`, `contentEncoding: base64` |
| `enum E` | inline `type: string` listing the known names (§5.4) |
| `repeated T` | `type: array`, `items: <T>` |
| `map<string, V>` | `type: object`, `additionalProperties: <V>` |
| `map<intN/bool, V>` | as above plus `propertyNames: {type: string, pattern: ...}` for the key's protojson form (nexgen requires the `type`) |
| `google.protobuf.Timestamp` | `type: string`, `format: date-time` |
| `google.protobuf.Duration` | `type: string`, `pattern: "^-?[0-9]+(\\.[0-9]{1,9})?s$"` |
| `google.protobuf.Struct` | `type: object`, `additionalProperties: true` (nexgen's open map) |
| `google.protobuf.Any` | `type: object`, `properties: {"@type": {type: string}}`, `required: ["@type"]`, open (Q3: nexgen accepts it and names the Go field `Type`) |
| `google.protobuf.Empty` | `type: object`, `properties: {}` |
| `google.protobuf.FieldMask` | `type: string` (comma-separated lowerCamel paths) |
| `google.protobuf.*Value` wrappers | the wrapped scalar's schema, e.g. `Int64Value` becomes the int64 string pattern. Not nullable: protojson omits an unset wrapper and never writes `null` (K2), and a `oneOf` with `null` would need the construct D4 avoids. |
| `google.protobuf.Value`, `ListValue`, `NullValue` | **error**: nexgen can't express "any JSON value". Not used today. |
| any message from a dependency package (e.g. `temporal.api.common.v1.Payload`) | emitted into that package's file like any other message (see below) |

**Dependency packages.** `temporal.api.common.v1` belongs to
[temporalio/api](https://github.com/temporalio/api), not this repo. The plugin
still emits `nexusrpc/temporal/api/common/v1/common.yaml`, but only for the
messages the closure reaches (today just `Payload`). That file is a projection
owned by this repo, regenerated from the version pinned in `buf.lock`. It is
not a schema for the whole upstream package. Bumping that dependency can
change the file, and that drift is expected; it is reviewed like any other
`nexusrpc/` diff. Its header says the source comes from a dependency module.
If temporalio/api ever publishes its own nexgen definitions, the plugin should
reference those instead of emitting a copy.

Usage today: 11 `Timestamp` imports, 2 `Duration`, 1 `Struct` (`auditlog`), 1 `Any`
(`operation`), 1 `int64`, 2 `bytes`, and 1 dependency message (`Payload`). No
wrapper types, `Value`, or non-string map keys are used. With the async-operation
filter (§5.8), `AsyncOperation` and the whole `operation` package are pruned, so
`Any` does not appear in the output today. Its mapping is still kept and tested.

### 5.4 Enums

An enum field is an inline string whose description lists the known names:

```yaml
state:
  description: |-
    <field comment>

    One of the `ResourceState` values:

    - `RESOURCE_STATE_UNSPECIFIED`
    - `RESOURCE_STATE_ACTIVE`: <value comment, on one line>
    - `RESOURCE_STATE_DELETED` (deprecated)

    Newer servers may return values not listed here.
  type: string
```

- **Open enums (D13).** CloudService regularly adds enum values, such as new
  resource states. A closed `enum` would make every generated client built
  before a value was added reject responses that carry it. nexgen has no open
  enum: it rejects `anyOf`, allows only objects in `$defs`, and rejects
  unknown `x-*` keywords such as the planned `x-enum-values` (Q8). So the type
  is a plain string, and the known names exist only as documentation. If nexgen
  gains an open enum, this is the one mapping to change.
- Enums are inlined at every use site, so an enum-only package (such as
  `temporal.api.cloud.resource.v1`) produces no file. For `repeated E` and
  `map<K, E>`, the list goes in the property's description, not on `items` or
  `additionalProperties`.
- Requests are unaffected in practice: protojson on the server rejects an enum
  name it doesn't know, exactly as it would for a proto client (K5).
- Enum aliases (`allow_alias`) are listed once, under the first name.
- `google.protobuf.NullValue` is an **error**, like `Value`.

### 5.5 Comments

- Leading comments become `description`. Trailing comments are appended as a
  new paragraph.
- Lines matching `^\s*temporal:[a-z_]+:` are removed, for example
  `temporal:versioning:min_version=v0.3.0` and
  `temporal:enums:replaces=state_deprecated`.
- Leading whitespace and the common indentation are stripped. Markdown is kept
  as written.
- Detached comments are ignored.

### 5.6 Deprecation (D7)

A field is emitted with `deprecated: true` when it has `[deprecated = true]` or
when its proto name ends in `_deprecated`, which is this repo's convention.
Messages with `option deprecated = true` get `deprecated: true` on their def.
Enums with `option deprecated = true` get `deprecated: true` too. JSON Schema
can't mark a single enum value as deprecated, so a value with
`[deprecated = true]` stays in the `enum` list and gets a `(deprecated)` suffix
on its line in the enum's `description` (§5.4). It isn't removed, because
protojson can still emit it. No enum value in the closure is deprecated today.

### 5.7 Handler codec (D14)

The schemas describe protojson, so D1 only holds if the handler that serves
these operations reads and writes protojson. The SDK's default data converter
does not:

- A nexgen client sends its input as a `json/plain` payload. The default
  converter decodes `json/plain` with `encoding/json`, which uses the
  snake_case `json:"namespace_id"` tags that protoc-gen-go generates. It also
  can't decode an int64 sent as a string, an RFC 3339 Timestamp, or a `"1.5s"`
  Duration into the generated Go types. Multi-word keys would be silently
  dropped, and the other cases fail.
- The SDK's `json/protobuf` converter does use protojson, but it is selected
  by payload metadata that a nexgen client doesn't set.

So the handler must, for every operation:

1. Decode the input payload's `data` with `protojson.Unmarshal` into the
   request message, whatever the payload's `encoding` metadata says. Unknown
   fields are rejected, not discarded, so a client using the wrong key fails
   loudly instead of having its input dropped.
2. Encode the response with `protojson.Marshal` using the default options
   (`json_name` keys, no `EmitUnpopulated`) and send it as `json/plain`.

3. Clear every field the filter excludes (§5.8) from the response before
   encoding it, so the output keys are exactly the schema's keys. The handler
   uses the same filter file and matcher as the plugin (`tools/nexgen/filter`,
   a non-internal package), so the two can't disagree. Excluded request fields
   never arrive: they aren't in the schema, and strict unmarshalling rejects
   them if a client sends them anyway.

The handler package provides this as a small shared codec, so individual
operation handlers can't pick a different converter by accident. M0 confirms
the default converter's behavior against the pinned SDK version and runs one
operation end to end: a nexgen-generated Go client calls a Go handler that uses
this codec. Q9 still covers where the handler runs. The codec rule applies
whatever the answer.

**Completing async operations.** Every mutating CloudService RPC starts an
async operation and returns an `AsyncOperation` handle. The filter removes that
handle from the contract (§5.8), so the Nexus operation's result must mean "the
operation finished". For each RPC whose proto response carries an
`async_operation`, the handler:

1. Sets the request's `async_operation_id` itself, derived deterministically
   from the Nexus request ID. A retried Nexus start then maps to the same
   CloudService operation instead of starting a second one, which replaces the
   idempotency the excluded client-supplied field used to provide.
2. Calls the RPC, then waits for the returned operation to reach a terminal
   state by polling `GetAsyncOperation`. The RPC is excluded from the contract
   but still called internally.
3. On `FULFILLED`, returns the RPC's original response (minus the excluded
   fields), not a re-read of the resource. On `FAILED`, `REJECTED`, or `CANCELLED`, fails
   the Nexus operation with a non-retryable error that carries the operation's
   `failure_reason`.

Operations such as namespace creation can take minutes, which is longer than a
synchronous Nexus handler may block. These are therefore served as Nexus
asynchronous operations, for example backed by a workflow. Whether an operation
runs sync or async is a handler choice that the definition file doesn't
express, so the generated contract is the same either way (Q9).

### 5.8 Filtering (D15)

Some of the proto surface should not be part of the Nexus contract. The
`filter=<path>` plugin option names a YAML file, relative to the directory buf
runs in (the repo root). The file lists what to exclude:

```yaml
# nexgen.filter.yaml: proto elements left out of the nexusrpc/ definitions.
exclude:
  # RPCs. Matched against the method's full name: <service fqn>.<Method>.
  methods:
    # Nexus operations return only after the async operation completes (§5.7),
    # so there is nothing left to poll.
    - temporal.api.cloud.cloudservice.v1.CloudService.GetAsyncOperation

  # Fields. Matched against the field's full name: <package>.<Message>[.<Nested>...].<field>.
  fields:
    # The client-supplied operation id on 48 mutating requests, and the "current
    # operation" id on resources (User, ServiceAccount, Namespace, Project, ...),
    # BillingReport, and auditlog LogRecord.
    - temporal.api.cloud.**.async_operation_id

  # Fields whose type is one of these messages or enums, wherever they appear.
  field_types:
    # The AsyncOperation handle on every mutating response.
    - temporal.api.cloud.operation.v1.AsyncOperation
```

**Patterns.** Names are compared as dot-separated segments. `*` matches exactly
one segment, and `**` matches zero or more. There are no other wildcards, and
names never contain partial-segment globs such as `Get*`. A plain name matches
only itself. For `field_types`, a `repeated T` or `map<K, T>` field matches when
`T` does.

**Semantics.**

- The filter runs on the mapped registry before the closure (§3, step 2b).
  Excluded fields are deleted from their message's `properties`, and excluded
  methods are dropped from `operations`. The closure then starts only from the
  remaining methods and follows only the remaining refs. Any type that was
  reachable only through excluded elements disappears with them. Here that is
  `GetAsyncOperationRequest`/`Response`, `AsyncOperation`, and so the whole
  `temporal.api.cloud.operation.v1` file.
- Diagnostics (§3) inside excluded or pruned elements are not reported, the
  same as for unreachable types. A filter can therefore also work around an
  unmappable field the contract doesn't need.
- A message whose properties are all excluded is still emitted as an empty
  `type: object`. Examples are `UpdateUserResponse` and `DeleteUserResponse`,
  whose only field is `async_operation`. This matches §5.1: the operation keeps
  an `output`, and fields can be added to it later without a breaking change.
- `oneof` notes (§5.2) list only the members that remain. If a single member
  remains, it gets no note.
- Messages and enums can't be excluded directly. A type is removed by
  excluding the fields and methods that reach it. This keeps the closure the
  only thing that decides which defs exist, and it avoids `$ref`s to defs that
  were removed.

**Safety.**

- Every rule must match at least one method or field in the plugin's input,
  reachable or not. A rule that matches nothing fails the run with
  `nexgen.filter.yaml: exclude.fields[0]: "<pattern>" matched nothing`. This
  catches typos and rules left behind after a proto rename.
- Excluding the input or output message of a method that is still included
  isn't possible, because messages can't be excluded. A method is either fully
  in the contract or fully out of it.
- The plugin doesn't check whether a filter breaks protos semantically, for
  example by removing a field a handler needs. That is the handler's job
  (§5.7). Every excluded request field must either be optional for the RPC or
  be filled in by the handler, as `async_operation_id` is.

**Effect on the contract.** The filter is part of the contract. Editing
`nexgen.filter.yaml` changes `nexusrpc/` like a proto change does, and
`nexgen-check` covers it. Including a previously excluded field or method is
additive and therefore compatible. Excluding a
field or method that was already published is a breaking change for generated
clients.

## 6. Naming

### 6.1 `$defs` keys

Each key is the message or enum name, with parent names concatenated for nested
types (§5.2). Keys only need to be unique within a package, because one file
covers one package. Any collision is reported as an error.

### 6.2 Property keys

Property keys are the `json_name` values. nexgen derives each language's
identifier from the key, so `namespaceId` becomes Go `NamespaceId`. Go
initialism fixes such as `x-go-name: NamespaceID` are left out of v1 (Q6).

### 6.3 Reserved words

nexgen fails at load time when a property or operation name maps to a
reserved identifier in a target language. When a key would collide, the plugin
emits `x-<lang>-name: <identifier>_` for that language only, using nexgen's
tokens `x-java-name`, `x-py-name`, and `x-ts-name` (Q7). The identifier compared
is the one each language derives from the key: lowerCamel for Java and
TypeScript, snake_case for Python.

The tables in `internal/naming/keywords.go` were measured against the pinned
nexgen by generating a one-property model for each candidate word:

- **Java**: the Java reserved words and literals, plus, for properties only,
  the locals of nexgen's generated deserializer (`items`, `index`, `node`, ...,
  copied from `JAVA_DESERIALIZER_LOCALS` in nexgen's source) and its numbered
  nested-array locals (`items1`, `element2`, ...).
- **Python**: the keywords and soft keywords (`match`, `case`).
- **TypeScript**: the reserved words, including `default` and `delete`.
- **Go**: none. nexgen exports Go identifiers, so a lower-case keyword never
  collides.

In the closure today this gives `x-java-name` and `x-ts-name` on
`CodecServerSpec.CustomErrorMessage.default`. When the pinned nexgen moves,
re-measure: `make nexgen-smoke` fails on any name the tables miss.

### 6.4 Type names across packages (D16)

nexgen generates Go, Python, and TypeScript into a single package or module,
so type names must be unique across every file, not just within one. Java
keeps a package per file.

- **Cross-package defs.** When a `$defs` key appears in more than one package
  in the output, each of those defs gets `x-go-name`, `x-py-name`, and
  `x-ts-name` set to the package segment in PascalCase plus the key. Today that
  is `LifecycleSpec` in `namespace` and `project`, which become
  `NamespaceLifecycleSpec` and `ProjectLifecycleSpec`. If that name is itself a
  def key, the run fails.
- **Inline shapes.** nexgen names an inline object property (a map, a Struct, an
  Any, an inline Empty) `<Model><Property>`. When that name is already a def
  key in the file, the plugin moves the shape into `$defs` as
  `<Model><Property>Map` (for a map or Struct) or `<Model><Property>Object`
  (otherwise) and refers to it with a `$ref`. Today that is
  `Namespace.regionStatus`, a `map<string, NamespaceRegionStatus>`, which
  becomes `NamespaceRegionStatusMap`.

## 7. Known divergences

The schema can accept or reject a payload differently from protojson in these
cases:

| # | Case | Effect |
|---|---|---|
| K1 | Two members of the same `oneof` are set | The schema accepts it, while protojson rejects it on unmarshal. |
| K2 | Non-canonical protojson input: int64 as a JSON number, an enum as an integer, `null` for a field, or the proto field name instead of `json_name` | The protojson parser accepts these, but the schema rejects them. **Senders must use the canonical form**, which is what `protojson.Marshal` and every Temporal SDK produce. |
| K3 | `NaN` or `±Infinity` in a `double`/`float` field | protojson encodes these as the strings `"NaN"` and `"Infinity"`, which `type: number` rejects. No current field is expected to carry them. |
| K4 | A Duration outside ±10000 years, or a Timestamp outside 0001–9999 | The pattern and format don't enforce protojson's range limits. |
| K5 | An enum name that the receiving protos don't define | The schema accepts any string (D13). On the server, `protojson.Unmarshal` rejects the request, which is the same as what a newer proto client sees against an older server. |
| K6 | A field excluded by the filter (§5.8) | The schema doesn't list it, but open objects (D6) still accept it. protojson accepts it too, but the handler rejects it in requests and clears it from responses (§5.7), so it never crosses the Nexus boundary. |

## 8. Build and CI integration

`buf.gen.nexgen.yaml` runs the plugin with `strategy: all`, the service
selection, and the filter. `nexgen.filter.yaml` sits next to it at the repo root,
and its content is the example in §5.8. `buf.yaml` excludes `tools/`, so the
plugin's test protos are not part of the API module (without that, every buf
command in the repo would compile them).

`Makefile` targets:

| Target | Does |
| --- | --- |
| `nexgen-build` | builds `.bin/protoc-gen-nexgen` from `tools/nexgen` |
| `nexgen-test` | `go test ./...` in `tools/nexgen` |
| `nexgen` | removes `nexusrpc/`, then `buf generate --template buf.gen.nexgen.yaml` |
| `nexgen-check` | `nexgen-test` and `nexgen`, then fails with "nexusrpc/ is out of date; run 'make nexgen' and commit the result" if git sees a change under `nexusrpc/` |
| `nexgen-install` | `cargo install` of nexgen at `NEXGEN_REV` into `.bin/` (nexgen has no release to download yet) |
| `nexgen-smoke` | runs nexgen on `nexusrpc/` for Go, Python, TypeScript, and Java |

`ci-build` gains `nexgen-check` and `nexgen-smoke`. `rm -rf nexusrpc` runs before
generation because a renamed package would otherwise leave an orphaned file
behind. CI's Go version comes from `tools/nexgen/go.mod`, the newest Go module in
the repo; buf v1.25.1 and the protoc plugins build with it, and buf v1.25.1
produces byte-identical output to current buf. CI needs `cargo`, which GitHub's
Ubuntu runners include.

`tools/nexgen` has its own `go.mod`. The repo root has no Go module (Go code is
generated into `.gen/` and published from a separate repo), so a module scoped
to the tool keeps its dependencies (`protoc-gen-star/v2`, `yaml.v3`,
`protocompile`) pinned in one place, and `go build`/`go test` work from
`tools/nexgen` without any setup at the root.

**Contributor workflow.** Because `nexgen-check` runs in `ci-build`, every PR
that changes a `.proto` must also commit the regenerated `nexusrpc/` files.
This needs only Go and buf, which contributors already have for `make proto`.
To keep it easy:

- The repo README's contributing section says to run `make nexgen` after
  editing protos.
- When `nexgen-check` fails, it prints `nexusrpc/ is out of date; run
  'make nexgen' and commit the result` before the diff.
- `.gitattributes` marks `nexusrpc/**` as `linguist-generated`, so GitHub
  collapses those files in PR diffs.

## 9. Testing

1. **Unit and golden tests** (`tools/nexgen/internal/...`). Each case covers one
   construct and lives in its own directory:

   ```
   testdata/cases/
   ├── scalars/            test.proto, params.txt (optional), golden/**/*.yaml
   ├── nested-types/
   ├── oneof/
   ├── presence/           proto3 optional vs implicit vs message fields
   ├── maps/
   ├── wkt/                every inlined WKT, and Empty as operation input/output
   ├── enums/              aliases, nested enums, acronym names
   ├── keyword-collision/
   ├── deprecation/
   ├── comment-directives/
   ├── closure-pruning/    unused message with an unsupported type → no error
   ├── cross-package/      moreprotos/*.proto imported by test.proto
   ├── filter-methods/     excluded RPC → its request/response pruned
   ├── filter-fields/      `*` / `**` patterns, nested messages, oneof note rewrite
   ├── filter-types/       field_types on singular, repeated, and map fields;
   │                       a package pruned entirely; an all-excluded message → empty object
   ├── filter-diagnostic/  unmappable field excluded → no error
   ├── cross-package-names/ a $defs key in two packages → x-*-name overrides (§6.4)
   ├── inline-shape-collision/ inline map/Struct named like a def → hoisted (§6.4)
   └── err-<name>/         test.proto + want_error.txt (diagnostic tests, item 2)
   ```

   Files under a case's `deps/` directory stand in for a dependency module:
   they are compiled but not in `file_to_generate`, like `temporal.api.common.v1`.
   A table-driven test lists `testdata/cases/*`, compiles each `test.proto`
   (and its imports in the same directory) with
   `github.com/bufbuild/protocompile`, and wraps the descriptors in a
   `pluginpb.CodeGeneratorRequest`. `params.txt` supplies plugin parameters.
   The request runs through the real PG\* pipeline in-process,
   `pgs.Init(pgs.ProtocInput(req), pgs.ProtocOutput(&resp)).RegisterModule(module.New()).Render()`,
   and the files in the resulting `CodeGeneratorResponse` are diffed against
   `golden/`. A case can include a `filter.yaml`, which `params.txt` refers to.
   `go test ./internal/module -update` rewrites the goldens (other
   packages don't define the flag). The plugin runs with the case directory as
   its working directory, so `filter=filter.yaml` names a file in the case. Adding a case means
   adding a directory, with no Go changes. This layout follows
   pubg/protoc-gen-jsonschema's `testdata/cases/`.
2. **Error tests.** Each diagnostic in §3 has an `err-*` case holding the
   expected message in `want_error.txt`. The filter adds
   `err-filter-unmatched`, `err-filter-bad-pattern`, and
   `err-filter-missing-file`. The matcher in `tools/nexgen/filter` also has
   its own table-driven unit tests, because the handler reuses it (§5.7).
   Diagnostics are returned in `CodeGeneratorResponse.error` (§3) rather than
   through PG\*'s `Fail`, which exits the process, so error cases run through
   the same in-process pipeline as golden cases. A case whose directory name
   starts with `err-` must fail, with exactly the text in `want_error.txt`.
3. **nexgen acceptance.** `make nexgen-smoke` runs nexgen for all four languages
   on the real output, and any construct nexgen rejects fails CI. With
   `NEXGEN=<path to nexgen>` set, `go test ./internal/module` also runs every
   golden case through nexgen in all four languages.
4. **protojson conformance.** This is the test that matters most. Objects are
   open and nothing is required (D5, D6), so validating against the schema
   only checks the types of keys the schema already knows. A misspelled or
   wrongly cased key validates fine. Conformance is therefore tested in three
   parts:
   1. **Key sets.** For every message in the closure, the def's `properties`
      keys must equal the message's protojson field names (`protoreflect`
      `FieldDescriptor.JSONName()`) minus the fields the filter excludes, with
      no keys missing and none extra. This check is deterministic and catches
      naming bugs directly. It also confirms that no excluded field leaks
      into the output.
   2. **Proto → schema.** A Go test fills every message with random values
      (`protorand` or a custom filler), clears the excluded fields with the
      handler's codec (§5.7), runs `protojson.Marshal`, and validates
      the result against the generated schema, either through nexgen's
      generated Go validator or a reference 2020-12 validator such as
      `santhosh-tekuri/jsonschema`. Every payload must pass, except the cases
      K1–K4 that are deliberately excluded.
   3. **Schema → proto.** JSON shaped by the schema is decoded with a strict
      `protojson.Unmarshal` (`DiscardUnknown: false`) into the proto message,
      and every value must survive. The JSON comes from the Go models that
      `nexgen-smoke` generates, filled randomly and passed to `json.Marshal`,
      so the test covers nexgen's own key and value encoding and not just the
      schema. Enum fields are filled from the known names only (K5), and
      oneofs with at most one member set (K1). This is the direction a real
      nexgen client takes through the handler (§5.7).

## 10. Milestones

| M | Scope | Exit criteria |
|---|---|---|
| M0 | *Partly done: the nexgen questions are answered (§11); the handler call is not.* **Spike against nexgen.** Hand-write a two-package sample and resolve Q1–Q4, Q7, and Q8. Run one operation end to end: a nexgen-generated Go client calls a Go handler that uses the protojson codec (§5.7), and the spike confirms the SDK's default-converter behavior that D14 assumes. | Each of those questions has an answer recorded in this doc, and the end-to-end call round-trips an int64, a Timestamp, an enum, and a multi-word key. |
| M1 | *Done.* Plugin skeleton, options, closure, and messages, scalars, enums, repeated fields, and maps in a single package. Golden tests. | Golden tests pass. |
| M2 | *Done.* Multiple packages and cross-file `$ref`, WKTs, oneof, deprecation, comments, keyword overrides, diagnostics, filtering (§5.8). | Real output passes `nexgen go`. |
| M3 | *Done.* `buf.gen.nexgen.yaml`, `nexgen.filter.yaml`, Makefile targets, CI, committed `nexusrpc/`. | `make ci-build` is green, the drift check works, and `nexusrpc/` has no `operation/v1` file, no `GetAsyncOperation` operation, and no `asyncOperation*` property. |
| M4 | protojson conformance suite (key sets, proto → schema, schema → proto), and nexgen acceptance in all four languages. | All three conformance parts pass apart from the documented divergences. |

## 11. Open questions

Q1–Q5, Q7, and Q8 were answered by measuring the pinned nexgen; they are kept
here with their answers.

| # | Question | Answer or status |
|---|---|---|
| Q1 | Does nexgen accept a file with only `$defs`, and resolve `other.yaml#/$defs/X`? | **Yes**, when nexgen is given the whole `nexusrpc/` directory as input. |
| Q2 | Does nexgen honor `description` and `deprecated` next to a `$ref`? | `deprecated` **yes**. `description` makes nexgen generate a copy of the type, so it is dropped (§5.2). |
| Q3 | Will nexgen accept a property named `@type`? | **Yes**; Go names the field `Type`. Not in today's output, because the filter prunes `AsyncOperation`. |
| Q4 | How does nexgen build Go and Java enum constant names? | **Moot**: enums are plain strings (D13). |
| Q5 | Do nexusrpc operations support `deprecated`? | **Yes**, on services and operations (§5.1). |
| Q6 | Should the plugin emit Go initialism overrides (`Id`→`ID`, `Url`→`URL`)? | Open. Doing so makes the generated Go idiomatic but adds many `x-go-name` keys. |
| Q7 | What are nexgen's reserved identifiers per language? | **Measured** (§6.3); the override keys are `x-java-name`, `x-py-name`, and `x-ts-name`. |
| Q8 | Does nexgen turn an `anyOf` open enum into a typed enum? | **No**: `anyOf` is rejected. The fallback is used (D13, §5.4). |
| Q9 | Which Nexus endpoint and handler will serve these operations, and do the handlers call `CloudService` over gRPC? Already decided: the codec (D14, §5.7) and that mutating operations complete before returning (D15, §5.7). Still open: what backs the Nexus async operations (a workflow per call?), the polling interval and timeout, and how a Nexus cancel maps to the CloudService operation. | Open; out of scope here. |
| Q10 | Should the request messages' `page_size`/`page_token` pagination get special treatment? | Open. |
| Q11 | Comments still mention async operations after the filter. For example, `NamespaceRegionStatus.State.STATE_FAILED` says "check failure_reason in the last async_operation status". Should the filter file also be able to override a `description`, or should these comments be reworded in the protos? | Open. |
| Q12 | Should field comments survive on `$ref` properties? nexgen forks a type for a `$ref` with a description (Q2). Wrapping as `allOf: [{$ref}]` was not tried. | Open. |
